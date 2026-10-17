package replica

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"github.com/sarchlab/mgpusim/v5/amd/driver"
	"sort"
)

type allocation struct {
	bytes uint64
	ctx   *driver.Context
}

type ManagedAllocation struct {
	Base  uint64 `json:"base"`
	Bytes uint64 `json:"bytes"`
}

// AttachCore adapts ordinary sample API submissions to drained global epochs.
// Allocation owners are static contiguous page partitions, independent of policy.
// Unified dispatch still targets distinct GPUs with independent MMUs/caches.
func AttachCore(s *Session, gpus []int) {
	s.Dynamic = true
	s.Stats.Adapter = "core-kernel-barrier-v1"
	if s.Passive != nil {
		s.Stats.Adapter = "core-kernel-internal-online-v1"
	}
	regions := map[driver.Ptr]allocation{}
	kernelID, hostID, freeID := 0, 0, 0
	digest := sha256.New()
	checkpoint := func() {
		e := s.Recorded.Epochs[len(s.Recorded.Epochs)-1]
		m := map[uint64]map[uint32]bool{}
		for _, a := range e.Accesses {
			if m[a.Page] == nil {
				m[a.Page] = map[uint32]bool{}
			}
			for _, x := range a.Writes {
				m[a.Page][x] = true
			}
		}
		pages := []uint64{}
		for p := range m {
			pages = append(pages, p)
		}
		sort.Slice(pages, func(i, j int) bool { return pages[i] < pages[j] })
		for _, p := range pages {
			for _, x := range sorted(m[p]) {
				buf, e := s.d.Resources().GlobalStorage.Read(s.pages[p].Home.PAddr+uint64(x)*s.Config.SectorBytes, s.Config.SectorBytes)
				must(e)
				must(binary.Write(digest, binary.LittleEndian, p))
				must(binary.Write(digest, binary.LittleEndian, x))
				_, e = digest.Write(buf)
				must(e)
			}
		}
		s.Stats.ValidationSHA256 = fmt.Sprintf("%x", digest.Sum(nil))
		if !s.TerminalDigest {
			return
		}
		terminal := sha256.New()
		bases := []uint64{}
		for p := range regions {
			bases = append(bases, uint64(p))
		}
		sort.Slice(bases, func(i, j int) bool { return bases[i] < bases[j] })
		for _, p := range bases {
			r := regions[driver.Ptr(p)]
			for pos := p; pos < p+r.bytes; {
				page := pos / s.Config.PageBytes * s.Config.PageBytes
				hi := min(p+r.bytes, page+s.Config.PageBytes)
				data, e := s.d.Resources().GlobalStorage.Read(s.pages[page].Home.PAddr+pos-page, hi-pos)
				must(e)
				_, e = terminal.Write(data)
				must(e)
				pos = hi
			}
		}
		s.Stats.TerminalSHA256 = fmt.Sprintf("%x", terminal.Sum(nil))
	}
	s.d.ReplicaBalancedDispatch = true
	s.d.ReplicaHooks = &driver.ReplicaHooks{
		Allocate: func(ctx *driver.Context, bytes uint64) driver.Ptr {
			s.d.WaitForEngineIdle()
			if ctx.PID() != s.ctx.PID() {
				panic("replica core requires a common address space")
			}
			if bytes == 0 {
				panic("zero-size managed allocation")
			}
			page := s.Config.PageBytes
			n := (bytes + page - 1) / page
			var base driver.Ptr
			for i, g := range gpus {
				lo, hi := (n*uint64(i)+uint64(len(gpus))-1)/uint64(len(gpus)), (n*uint64(i+1)+uint64(len(gpus))-1)/uint64(len(gpus))
				if hi == lo {
					continue
				}
				p := s.d.AllocateUnifiedMemoryOnGPU(ctx, (hi-lo)*page, g)
				if base == 0 {
					base = p - driver.Ptr(lo*page)
				}
				if p != base+driver.Ptr(lo*page) {
					panic("noncontiguous managed allocation")
				}
			}
			s.Manage(base, bytes)
			for p := uint64(base); p < uint64(base)+bytes; p += page {
				must(s.d.Resources().GlobalStorage.Write(s.pages[p].Home.PAddr, make([]byte, page)))
			}
			regions[base] = allocation{bytes, ctx}
			s.Stats.CoreAllocations = append(s.Stats.CoreAllocations, ManagedAllocation{uint64(base), bytes})
			live := uint64(0)
			for _, st := range s.pages {
				if !st.Retired {
					live++
				}
			}
			s.Stats.PeakLiveManagedPages = max(s.Stats.PeakLiveManagedPages, live)
			return base
		},
		Kernel: func(q *driver.CommandQueue, work func()) {
			kernelID++
			if s.Passive != nil {
				s.Passive.kernel = kernelID
			}
			s.Epoch(fmt.Sprintf("kernel/%06d", kernelID), work)
			checkpoint()
		},
		HostWrite: func(q *driver.CommandQueue, ptr driver.Ptr, src interface{}, work func()) bool {
			size := binary.Size(src)
			if size < 0 {
				panic("unsupported H2D source")
			}
			end := uint64(ptr) + uint64(size)
			managed := false
			for base, r := range regions {
				if uint64(ptr) < uint64(base)+r.bytes && end > uint64(base) {
					managed = true
					break
				}
			}
			if !managed {
				return false
			}
			s.d.WaitForEngineIdle()
			// Before the first kernel, initial data establish version zero.
			if s.at == 0 {
				work()
				return true
			}
			hostID++
			s.phaseKind = "host-write"
			defer func() { s.phaseKind = "" }()
			s.Epoch(fmt.Sprintf("host-write/%06d", hostID), func() {
				for pos := uint64(ptr); pos < end; {
					page := pos / s.Config.PageBytes * s.Config.PageBytes
					hi := min(end, page+s.Config.PageBytes)
					if st := s.pages[page]; st != nil && !st.Retired {
						s.observe(int(st.Home.DeviceID), pos, hi-pos, nil, true)
						if s.Passive != nil {
							bits := map[uint32]bool{}
							for offset := pos - page; offset < hi-page; offset++ {
								bits[uint32(offset/s.Config.SectorBytes)] = true
							}
							s.Passive.HostWrite(page, bits)
						}
					}
					pos = hi
				}
				work()
			})
			checkpoint()
			return true
		},
		Free: func(ctx *driver.Context, ptr driver.Ptr) bool {
			r, ok := regions[ptr]
			if !ok {
				return false
			}
			s.d.WaitForEngineIdle()
			freeID++
			s.phaseKind = "retire"
			defer func() { s.phaseKind = "" }()
			retired := []uint64{}
			s.Epoch(fmt.Sprintf("retire/%06d", freeID), func() {
				s.invalidateTLBs()
				for p := uint64(ptr); p < uint64(ptr)+r.bytes; p += s.Config.PageBytes {
					st := s.pages[p]
					for g, c := range st.Copies {
						s.release(g, c)
					}
					s.tables.RetireAtBarrier(ctx.PID(), p)
					s.d.ReleaseReplicaFrame(int(st.Home.DeviceID), st.Home.PAddr)
					st.Retired = true
					s.Stats.RetiredManagedPages++
					retired = append(retired, p)
				}
			})
			s.Recorded.Epochs[len(s.Recorded.Epochs)-1].RetiredPages = retired
			s.d.RetireReplicaBuffer(ctx, ptr, r.bytes)
			delete(regions, ptr)
			return true
		},
	}
}
