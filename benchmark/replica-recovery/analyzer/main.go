// rranalyze replays an MGPUSim memtrace (see amd/timing/memtrace) against
// three page-replication policies at kernel-boundary epochs and reports the
// ideal opportunity of recovering invalidated replicas by delta repair.
//
//	INV    write epoch collapses the page to one home copy; other replicas
//	       are dropped and a later read copies the whole page again.
//	RETAIN same state transitions as INV, but dropped replicas are kept with a
//	       version; a later read copies only sectors written since (oracle).
//	UPD    GPS-like: replicas stay valid, every write epoch pushes the
//	       written blocks to all subscribers (coalesced per epoch).
package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/bits"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	sectorShift = 5 // 32 B tracking granularity
	maxGPU      = 8 // bit 0 is the host
)

var (
	tracePrefix = flag.String("trace", "", "memtrace prefix (<prefix>.bin, <prefix>.meta)")
	pageList    = flag.String("pages", "4096,65536,2097152", "page sizes in bytes")
	granList    = flag.String("grans", "32,128,512,4096", "repair granularities in bytes")
	outJSON     = flag.String("json", "", "write full results as JSON")
	useChanged  = flag.Bool("changed", false, "use value-changed masks ('C', emulation traces) instead of written masks as the dirty set")
	dumpPage    = flag.Uint64("dump", 0, "print per-epoch page readers/writers at this page size and exit")
)

type kernel struct {
	id         uint64
	name       string
	start, end uint64
	host       bool
	hostAddr   uint64
	hostSize   uint64
}

type bufRange struct{ lo, hi uint64 }

type secAcc struct {
	r, w   uint8
	rc, wc [maxGPU]uint32
}

func main() {
	flag.Parse()
	if *tracePrefix == "" {
		fmt.Fprintln(os.Stderr, "need -trace")
		os.Exit(2)
	}

	epochs, bufs := readMeta(*tracePrefix + ".meta")
	sims := []*sim{}
	for _, p := range parseList(*pageList) {
		sims = append(sims, newSim(p, parseList(*granList), epochs, bufs))
	}

	f, err := os.Open(*tracePrefix + ".bin")
	check(err)
	defer f.Close()
	rd := bufio.NewReaderSize(f, 1<<22)

	cur := -1
	acc := map[uint64]*secAcc{}
	flush := func() {
		if cur >= 0 && *dumpPage != 0 {
			dump(cur, acc, *dumpPage)
		} else if cur >= 0 {
			for _, s := range sims {
				s.epoch(cur, acc)
			}
		}
		acc = map[uint64]*secAcc{}
	}
	advanceTo := func(target int) {
		for cur < target {
			flush()
			cur++
			if epochs[cur].host {
				addHostWrite(acc, epochs[cur])
			}
		}
	}

	var stats struct{ total, user, outside, other uint64 }
	var perGPU [maxGPU]uint64
	var rec [32]byte
	bi := 0
	for {
		if _, err := io.ReadFull(rd, rec[:]); err != nil {
			if err == io.EOF {
				break
			}
			check(err)
		}
		stats.total++
		kind := rec[0]
		gpu := rec[1]
		if gpu >= maxGPU {
			panic("trace GPU id exceeds analyzer capacity")
		}
		t := binary.LittleEndian.Uint64(rec[8:])
		addr := binary.LittleEndian.Uint64(rec[16:])
		payload := binary.LittleEndian.Uint64(rec[24:])

		target := cur
		for target+1 < len(epochs) && epochs[target+1].start <= t {
			target++
		}
		advanceTo(target)
		if cur < 0 || t > epochs[cur].end {
			stats.outside++
			continue
		}

		if !inBufs(bufs, &bi, addr) {
			stats.other++
			continue
		}
		stats.user++
		perGPU[gpu]++

		if kind == 'C' || kind == 'W' {
			// 'C' (value changed) and 'W' (written) describe the same write.
			// Use one as the dirty set; the other still marks a write access.
			if kind == 'W' {
				getAcc(acc, addr>>sectorShift).wc[gpu]++
			}
			if (kind == 'C') != *useChanged {
				continue
			}
			for m := payload; m != 0; m &= m - 1 {
				i := uint64(bits.TrailingZeros64(m))
				getAcc(acc, (addr+i)>>sectorShift).w |= 1 << gpu
			}
		} else {
			lo := addr >> sectorShift
			hi := (addr + payload - 1) >> sectorShift
			for s := lo; s <= hi; s++ {
				getAcc(acc, s).r |= 1 << gpu
			}
			getAcc(acc, lo).rc[gpu]++
		}
	}
	advanceTo(len(epochs) - 1)
	flush()

	fmt.Printf("trace %s: %d records, %d user, %d non-user, %d outside kernels; %d epochs\n",
		*tracePrefix, stats.total, stats.user, stats.other, stats.outside, len(epochs))

	fmt.Printf("user records per GPU: %v\n", perGPU[1:5])

	results := []map[string]interface{}{}
	for _, s := range sims {
		s.finish()
		s.print()
		results = append(results, s.result())
	}
	if *outJSON != "" {
		b, _ := json.MarshalIndent(map[string]interface{}{
			"trace": *tracePrefix, "records": stats.total, "user": stats.user,
			"epochs": len(epochs), "results": results}, "", " ")
		check(os.WriteFile(*outJSON, b, 0o644))
	}
}

func getAcc(acc map[uint64]*secAcc, s uint64) *secAcc {
	a := acc[s]
	if a == nil {
		a = &secAcc{}
		acc[s] = a
	}
	return a
}

func addHostWrite(acc map[uint64]*secAcc, k kernel) {
	if k.hostSize == 0 {
		return
	}
	for s := k.hostAddr >> sectorShift; s <= (k.hostAddr+k.hostSize-1)>>sectorShift; s++ {
		getAcc(acc, s).w |= 1
	}
	getAcc(acc, k.hostAddr>>sectorShift).wc[0]++
}

func inBufs(bufs []bufRange, hint *int, a uint64) bool {
	if *hint < len(bufs) && a >= bufs[*hint].lo && a < bufs[*hint].hi {
		return true
	}
	i := sort.Search(len(bufs), func(i int) bool { return bufs[i].hi > a })
	if i < len(bufs) && a >= bufs[i].lo {
		*hint = i
		return true
	}
	return false
}

func readMeta(path string) ([]kernel, []bufRange) {
	f, err := os.Open(path)
	check(err)
	defer f.Close()
	sc := bufio.NewScanner(f)
	open := map[uint64]int{}
	var ks []kernel
	var bufs []bufRange
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 2 {
			continue
		}
		t := atou(fs[0])
		switch fs[1] {
		case "U":
			a, n := atou(fs[2]), atou(fs[3])
			bufs = append(bufs, bufRange{a, a + n})
		case "K":
			id := atou(fs[2])
			open[id] = len(ks)
			ks = append(ks, kernel{id: id, name: fs[3], start: t, end: ^uint64(0)})
		case "H":
			ks = append(ks, kernel{id: atou(fs[2]), name: "H2D", start: t, end: t,
				host: true, hostAddr: atou(fs[3]), hostSize: atou(fs[4])})
		case "E":
			if i, ok := open[atou(fs[2])]; ok {
				ks[i].end = t
				delete(open, atou(fs[2]))
			}
		}
	}
	sort.Slice(bufs, func(i, j int) bool { return bufs[i].lo < bufs[j].lo })
	// Keep only host copies that overlap user buffers, clipped to them.
	kept := ks[:0]
	for _, k := range ks {
		if k.host {
			lo, hi := k.hostAddr, k.hostAddr+k.hostSize
			ok := false
			for _, b := range bufs {
				if lo < b.hi && hi > b.lo {
					ok = true
				}
			}
			if !ok {
				continue
			}
		}
		kept = append(kept, k)
	}
	ks = kept
	sort.SliceStable(ks, func(i, j int) bool { return ks[i].start < ks[j].start })
	for i := 1; i < len(ks); i++ {
		if !ks[i-1].host && ks[i-1].end > ks[i].start {
			fmt.Fprintf(os.Stderr, "warning: epochs %d (%s) and %d overlap\n", i-1, ks[i-1].name, i)
		}
	}
	return ks, bufs
}

func atou(s string) uint64 {
	v, err := strconv.ParseUint(s, 10, 64)
	check(err)
	return v
}

func parseList(s string) []uint64 {
	var out []uint64
	for _, f := range strings.Split(s, ",") {
		out = append(out, atou(strings.TrimSpace(f)))
	}
	return out
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func dump(e int, acc map[uint64]*secAcc, P uint64) {
	type rw struct{ r, w uint8 }
	m := map[uint64]rw{}
	for sec, a := range acc {
		p := (sec << sectorShift) / P
		x := m[p]
		x.r |= a.r
		x.w |= a.w
		m[p] = x
	}
	keys := make([]uint64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		fmt.Printf("%d %d r%08b w%08b\n", e, k, m[k].r, m[k].w)
	}
}
