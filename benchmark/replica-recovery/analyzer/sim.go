package main

import (
	"fmt"
	"math/bits"
	"sort"
)

type pageEpoch struct {
	readers, writers uint8
	rtx, wtx         [maxGPU]uint32
	union            []uint32
	byReader         [maxGPU][]uint32
	byWriter         [maxGPU][]uint32
}

type pageState struct {
	home        int8
	valid, had  uint8
	version     [maxGPU]int32
	staleSince  [maxGPU]int32
	open        [maxGPU]int32
	lastWrite   []int32
	writeEpochs []int32

	subs       uint8
	updPending [][]uint64 // [gpu][gran] bytes pushed since the GPU's last read
}

type refill struct {
	write            bool
	blocks           []uint32 // dirty blocks per granularity
	extents          []uint32 // contiguous dirty runs per granularity
	merged           int32    // write epochs folded into this repair
	distEpochs       int32
	distTime         uint64
	reads            uint64 // read transactions served by the restored replica
	readDirtySectors uint64
	readCleanSectors uint64
}

type sim struct {
	P          uint64
	secPerPage int
	pgShift    uint
	grans      []uint64
	gShift     []uint
	epochs     []kernel
	pages      map[uint64]*pageState

	readHit, cold, coldWrite uint64
	refills                  []refill
	invalidations            uint64
	neverReused              uint64
	neverDist                []int32
	remoteTx                 uint64
	s3Eligible               uint64
	s3NoReplica              uint64
	s3Conflict               uint64
	s3CleanSectors           uint64
	writePageEpochs          uint64
	multiWriterPageEpochs    uint64

	staleCnt, staleMax [maxGPU]int64
	staleSum           [maxGPU]int64
	nEpochs            int64

	updFillCold  uint64
	updBytes     []uint64
	updNeverRead []uint64
	updMsgs      uint64
}

func newSim(P uint64, grans []uint64, epochs []kernel, _ []bufRange) *sim {
	s := &sim{P: P, epochs: epochs, pages: map[uint64]*pageState{}}
	s.secPerPage = int(P >> sectorShift)
	s.pgShift = uint(bits.TrailingZeros64(P)) - sectorShift
	for _, g := range grans {
		if g >= 1<<sectorShift && g <= P {
			s.grans = append(s.grans, g)
			s.gShift = append(s.gShift, uint(bits.TrailingZeros64(g))-sectorShift)
		}
	}
	s.updBytes = make([]uint64, len(s.grans))
	s.updNeverRead = make([]uint64, len(s.grans))
	return s
}

func (s *sim) state(p uint64) *pageState {
	st := s.pages[p]
	if st == nil {
		st = &pageState{home: -1}
		for g := range st.open {
			st.open[g] = -1
			st.staleSince[g] = -1
		}
		s.pages[p] = st
	}
	return st
}

func (s *sim) epoch(e int, acc map[uint64]*secAcc) {
	pes := map[uint64]*pageEpoch{}
	for sec, a := range acc {
		p := sec >> s.pgShift
		off := uint32(sec & uint64(s.secPerPage-1))
		pe := pes[p]
		if pe == nil {
			pe = &pageEpoch{}
			pes[p] = pe
		}
		pe.readers |= a.r
		pe.writers |= a.w
		for g := 0; g < maxGPU; g++ {
			if a.r&(1<<g) != 0 {
				pe.byReader[g] = append(pe.byReader[g], off)
			}
			pe.rtx[g] += a.rc[g]
			pe.wtx[g] += a.wc[g]
			if a.w&(1<<g) != 0 {
				pe.byWriter[g] = append(pe.byWriter[g], off)
			}
		}
		if a.w != 0 {
			pe.union = append(pe.union, off)
		}
	}

	for p, pe := range pes {
		st := s.state(p)
		s.updEpoch(st, pe)
		if pe.writers == 0 {
			for m := pe.readers; m != 0; m &= m - 1 {
				g := bits.TrailingZeros8(m)
				s.readPhase(st, g, e, uint64(pe.rtx[g]), false, pe.byReader[g])
			}
			continue
		}
		s.writeEpoch(st, pe, e)
	}

	s.nEpochs++
	for g := 1; g < maxGPU; g++ {
		s.staleSum[g] += s.staleCnt[g]
		if s.staleCnt[g] > s.staleMax[g] {
			s.staleMax[g] = s.staleCnt[g]
		}
	}
}

func (s *sim) chooseHome(st *pageState, pe *pageEpoch) int {
	if st.home >= 0 && pe.writers&(1<<st.home) != 0 && st.valid&(1<<st.home) != 0 {
		return int(st.home)
	}
	best, bestTx, bestValid := -1, uint32(0), false
	for m := pe.writers; m != 0; m &= m - 1 {
		g := bits.TrailingZeros8(m)
		v := st.valid&(1<<g) != 0
		if best < 0 || (v && !bestValid) || (v == bestValid && pe.wtx[g] > bestTx) {
			best, bestTx, bestValid = g, pe.wtx[g], v
		}
	}
	return best
}

func (s *sim) writeEpoch(st *pageState, pe *pageEpoch, e int) {
	s.writePageEpochs++
	if bits.OnesCount8(pe.writers) > 1 {
		s.multiWriterPageEpochs++
	}
	h := s.chooseHome(st, pe)
	hb := uint8(1) << h

	if st.home < 0 {
		s.coldWrite++
		st.home = int8(h)
		st.had |= hb
		st.valid |= hb
		st.version[h] = int32(e)
	} else if st.valid&hb == 0 {
		s.readPhase(st, h, e, 0, true, pe.byReader[h])
	}
	if o := st.open[h]; o >= 0 {
		s.refills[o].reads += uint64(pe.rtx[h])
	}

	for g := 0; g < maxGPU; g++ {
		if g != h {
			s.remoteTx += uint64(pe.rtx[g] + pe.wtx[g])
		}
	}
	dirty := map[uint32]bool{}
	for _, x := range pe.union {
		dirty[x] = true
	}
	for g := 1; g < maxGPU; g++ {
		if g == h {
			continue
		}
		if st.valid&(1<<g) == 0 {
			s.s3NoReplica += uint64(pe.rtx[g])
			continue
		}
		conflict := false
		for _, x := range pe.byReader[g] {
			if dirty[x] {
				conflict = true
			} else {
				s.s3CleanSectors++
			}
		}
		if conflict || bits.OnesCount8(pe.writers) > 1 {
			s.s3Conflict += uint64(pe.rtx[g])
		} else {
			s.s3Eligible += uint64(pe.rtx[g])
		}
	}

	for m := st.valid &^ hb; m != 0; m &= m - 1 {
		g := bits.TrailingZeros8(m)
		st.open[g] = -1
		if g == 0 {
			continue
		}
		st.staleSince[g] = int32(e)
		s.invalidations++
		s.staleCnt[g]++
	}
	st.valid = hb
	st.home = int8(h)
	st.version[h] = int32(e)

	if st.lastWrite == nil {
		st.lastWrite = make([]int32, s.secPerPage)
		for i := range st.lastWrite {
			st.lastWrite[i] = -1
		}
	}
	for _, off := range pe.union {
		st.lastWrite[off] = int32(e)
	}
	st.writeEpochs = append(st.writeEpochs, int32(e))
}

func (s *sim) readPhase(st *pageState, g, e int, reads uint64, forWrite bool, readSectors []uint32) {
	b := uint8(1) << g
	if st.valid&b != 0 {
		s.readHit++
		if o := st.open[g]; o >= 0 {
			s.refills[o].reads += reads
		}
		return
	}
	if st.had&b == 0 {
		if forWrite {
			s.coldWrite++
		} else {
			s.cold++
		}
		st.had |= b
		st.valid |= b
		st.version[g] = int32(e)
		if st.home < 0 {
			st.home = int8(g)
		}
		return
	}

	v := st.version[g]
	r := refill{write: forWrite, reads: reads,
		blocks: make([]uint32, len(s.grans)), extents: make([]uint32, len(s.grans))}
	for _, x := range readSectors {
		if st.lastWrite != nil && st.lastWrite[x] > v {
			r.readDirtySectors++
		} else {
			r.readCleanSectors++
		}
	}
	since := st.staleSince[g]
	if since >= 0 {
		r.distEpochs = int32(e) - since
		r.distTime = s.epochs[e].start - s.epochs[since].start
	}
	for _, we := range st.writeEpochs {
		if we > v {
			r.merged++
		}
	}
	for gi, sh := range s.gShift {
		per := 1 << sh
		prev := false
		for blk := 0; blk < s.secPerPage/per; blk++ {
			dirty := false
			for i := blk * per; i < (blk+1)*per; i++ {
				if st.lastWrite[i] > v {
					dirty = true
					break
				}
			}
			if dirty {
				r.blocks[gi]++
				if !prev {
					r.extents[gi]++
				}
			}
			prev = dirty
		}
	}
	s.refills = append(s.refills, r)
	st.open[g] = int32(len(s.refills) - 1)
	st.valid |= b
	st.version[g] = int32(e)
	if since >= 0 && g != 0 {
		s.staleCnt[g]--
	}
	st.staleSince[g] = -1
}

// updEpoch applies the GPS-like update policy to one page epoch.
func (s *sim) updEpoch(st *pageState, pe *pageEpoch) {
	if st.updPending == nil {
		st.updPending = make([][]uint64, maxGPU)
	}
	for m := pe.readers; m != 0; m &= m - 1 {
		g := bits.TrailingZeros8(m)
		if st.subs&(1<<g) == 0 {
			st.subs |= 1 << g
			s.updFillCold++
		}
		st.updPending[g] = nil
	}
	for m := pe.writers; m != 0; m &= m - 1 {
		w := bits.TrailingZeros8(m)
		targets := st.subs &^ (1 << w)
		if targets == 0 {
			continue
		}
		blocks := make([]uint64, len(s.grans))
		for gi, sh := range s.gShift {
			seen := map[uint32]bool{}
			for _, off := range pe.byWriter[w] {
				seen[off>>sh] = true
			}
			blocks[gi] = uint64(len(seen)) * s.grans[gi]
		}
		for t := targets; t != 0; t &= t - 1 {
			g := bits.TrailingZeros8(t)
			s.updMsgs++
			if st.updPending[g] == nil {
				st.updPending[g] = make([]uint64, len(s.grans))
			}
			for gi := range s.grans {
				s.updBytes[gi] += blocks[gi]
				st.updPending[g][gi] += blocks[gi]
			}
		}
	}
}

func (s *sim) finish() {
	last := int32(len(s.epochs) - 1)
	for _, st := range s.pages {
		for g := 1; g < maxGPU; g++ {
			if st.staleSince[g] >= 0 {
				s.neverReused++
				s.neverDist = append(s.neverDist, last-st.staleSince[g])
			}
			if st.updPending != nil && st.updPending[g] != nil {
				for gi := range s.grans {
					s.updNeverRead[gi] += st.updPending[g][gi]
				}
			}
		}
	}
}

func pct(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return 100 * a / b
}

func quantile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sort.Float64s(xs)
	return xs[int(q*float64(len(xs)-1))]
}

type bin struct {
	label  string
	lo, hi float64 // [lo, hi)
}

func histogram(xs []float64, bins []bin) []float64 {
	out := make([]float64, len(bins))
	for _, x := range xs {
		for i, b := range bins {
			if x >= b.lo && x < b.hi {
				out[i]++
				break
			}
		}
	}
	for i := range out {
		out[i] = pct(out[i], float64(len(xs)))
	}
	return out
}

var fracBins = []bin{{"<=1%", 0, 0.01 + 1e-12}, {"1-5%", 0.01 + 1e-12, 0.05 + 1e-12},
	{"5-10%", 0.05 + 1e-12, 0.10 + 1e-12}, {"10-25%", 0.10 + 1e-12, 0.25 + 1e-12},
	{"25-50%", 0.25 + 1e-12, 0.5 + 1e-12}, {"50-99%", 0.5 + 1e-12, 1 - 1e-12}, {"100%", 1 - 1e-12, 2}}
var countBins = []bin{{"1", 1, 2}, {"2", 2, 3}, {"3-4", 3, 5}, {"5-16", 5, 17}, {">16", 17, 1e18}}

func (s *sim) result() map[string]interface{} {
	nR := float64(len(s.refills))
	res := map[string]interface{}{
		"page": s.P, "grans": s.grans, "pages_touched": len(s.pages),
		"read_hit": s.readHit, "cold_read": s.cold, "cold_write": s.coldWrite,
		"refills": len(s.refills), "invalidations": s.invalidations,
		"never_reused": s.neverReused, "remote_tx_write_epochs": s.remoteTx,
		"s3_preexisting_replica_read_transactions":      s.s3Eligible,
		"s3_no_preexisting_replica_read_transactions":   s.s3NoReplica,
		"s3_conflicting_or_ambiguous_read_transactions": s.s3Conflict,
		"s3_preexisting_replica_clean_read_sectors":     s.s3CleanSectors,
		"write_page_epochs":                             s.writePageEpochs, "multi_writer_page_epochs": s.multiWriterPageEpochs,
		"upd_fill_cold": s.updFillCold, "upd_bytes": s.updBytes,
		"upd_never_read_bytes": s.updNeverRead, "upd_msgs": s.updMsgs,
	}
	nw := 0
	for _, r := range s.refills {
		if r.write {
			nw++
		}
	}
	res["refills_for_write"] = nw

	retain := make([]uint64, len(s.grans))
	extents := make([]float64, len(s.grans))
	fracs := make([][]float64, len(s.grans))
	var merged, dist, reads []float64
	for _, r := range s.refills {
		for gi := range s.grans {
			retain[gi] += uint64(r.blocks[gi]) * s.grans[gi]
			extents[gi] += float64(r.extents[gi])
			fracs[gi] = append(fracs[gi], float64(r.blocks[gi])*float64(s.grans[gi])/float64(s.P))
		}
		merged = append(merged, float64(r.merged))
		dist = append(dist, float64(r.distEpochs))
		reads = append(reads, float64(r.reads))
	}
	var rd, rc uint64
	for _, r := range s.refills {
		rd += r.readDirtySectors
		rc += r.readCleanSectors
	}
	res["reread_dirty_sectors"] = rd
	res["reread_clean_sectors"] = rc
	res["inv_refill_bytes"] = uint64(len(s.refills)) * s.P
	res["retain_refill_bytes"] = retain
	fh := map[string]interface{}{}
	for gi, g := range s.grans {
		fh[fmt.Sprint(g)] = map[string]interface{}{
			"hist":         histogram(append([]float64(nil), fracs[gi]...), fracBins),
			"p50":          quantile(append([]float64(nil), fracs[gi]...), 0.5),
			"p90":          quantile(append([]float64(nil), fracs[gi]...), 0.9),
			"mean_extents": extents[gi] / nR,
		}
	}
	res["dirty_fraction"] = fh
	res["merged_hist"] = histogram(merged, countBins)
	res["merged_mean"] = mean(merged)
	res["reuse_dist_hist"] = histogram(dist, countBins)
	res["reads_per_refill_p50"] = quantile(append([]float64(nil), reads...), 0.5)
	res["reads_per_refill_mean"] = mean(reads)
	var staleAvg, staleMax float64
	for g := 1; g < maxGPU; g++ {
		if s.nEpochs > 0 {
			staleAvg += float64(s.staleSum[g]) / float64(s.nEpochs)
		}
		staleMax += float64(s.staleMax[g])
	}
	res["stale_pages_avg_sum_gpus"] = staleAvg
	res["stale_pages_peak_sum_gpus"] = staleMax
	return res
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	t := 0.0
	for _, x := range xs {
		t += x
	}
	return t / float64(len(xs))
}

func human(b float64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2fGB", b/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.2fMB", b/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1fKB", b/(1<<10))
	}
	return fmt.Sprintf("%.0fB", b)
}

func (s *sim) print() {
	r := s.result()
	nR := len(s.refills)
	fmt.Printf("\n== page %s: %d pages touched ==\n", human(float64(s.P)), len(s.pages))
	fmt.Printf("GPU page-epoch reads: hit %d, cold %d (+%d write-first), refill %d (%d for write)\n",
		s.readHit, s.cold, s.coldWrite, nR, r["refills_for_write"])
	fmt.Printf("invalidations %d: re-read %d, never re-read %d (%.1f%%)\n",
		s.invalidations, uint64(nR)-uint64(r["refills_for_write"].(int)), s.neverReused,
		pct(float64(s.neverReused), float64(s.invalidations)))
	fmt.Printf("write page-epochs %d (%.1f%% multi-writer); remote tx in write epochs %d\n",
		s.writePageEpochs, pct(float64(s.multiWriterPageEpochs), float64(s.writePageEpochs)), s.remoteTx)
	if nR == 0 {
		fmt.Println("no refills")
		return
	}
	inv := float64(r["inv_refill_bytes"].(uint64))
	fmt.Printf("INV refill bytes %s\n", human(inv))
	fmt.Printf("%-8s %12s %8s %12s %12s %8s %8s  dirty-fraction histogram %v\n",
		"gran", "RETAIN", "%INV", "UPD push", "UPD->never", "p50", "extents", labels(fracBins))
	fh := r["dirty_fraction"].(map[string]interface{})
	for gi, g := range s.grans {
		d := fh[fmt.Sprint(g)].(map[string]interface{})
		rb := float64(r["retain_refill_bytes"].([]uint64)[gi])
		fmt.Printf("%-8s %12s %7.1f%% %12s %12s %7.1f%% %8.1f  %s\n", human(float64(g)),
			human(rb), pct(rb, inv), human(float64(s.updBytes[gi])),
			human(float64(s.updNeverRead[gi])), 100*d["p50"].(float64),
			d["mean_extents"].(float64), fmtHist(d["hist"].([]float64)))
	}
	fmt.Printf("write epochs merged per repair (mean %.2f) %v: %s\n", r["merged_mean"],
		labels(countBins), fmtHist(r["merged_hist"].([]float64)))
	fmt.Printf("reuse distance in epochs %v: %s\n", labels(countBins),
		fmtHist(r["reuse_dist_hist"].([]float64)))
	fmt.Printf("reads per restored replica p50 %.0f mean %.1f\n",
		r["reads_per_refill_p50"], r["reads_per_refill_mean"])
	fmt.Printf("stale replicas retained (sum over GPUs): avg %.1f pages, peak %.0f pages (footprint %d pages)\n",
		r["stale_pages_avg_sum_gpus"], r["stale_pages_peak_sum_gpus"], len(s.pages))
}

func labels(bs []bin) []string {
	var out []string
	for _, b := range bs {
		out = append(out, b.label)
	}
	return out
}

func fmtHist(h []float64) string {
	out := ""
	for _, v := range h {
		out += fmt.Sprintf("%5.1f ", v)
	}
	return out
}
