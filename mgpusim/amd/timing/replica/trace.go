package replica

import (
	"github.com/sarchlab/akita/v5/mem/memprotocol"
	"github.com/sarchlab/akita/v5/tracing"
)

type accessTracer struct {
	tracing.NopTracer
	session *Session
	gpu     int
}

func (t *accessTracer) StartTask(task tracing.TaskStart) {
	s := t.session
	if s.active == nil || task.Kind != "req_in" {
		return
	}
	switch a := task.Detail.(type) {
	case memprotocol.ReadReq:
		s.observe(t.gpu, a.Address, a.AccessByteSize, nil, false)
	case memprotocol.WriteReq:
		s.observe(t.gpu, a.Address, uint64(len(a.Data)), a.DirtyMask, true)
	}
}

func (s *Session) observe(gpu int, addr, n uint64, mask []bool, write bool) {
	// Process a request across every covered page. Byte masks are used for writes;
	// reads preserve every touched sector, not just the starting sector.
	masks := map[uint64]map[uint32]bool{}
	for off := uint64(0); off < n; off++ {
		if write && len(mask) > 0 && !mask[off] {
			continue
		}
		a := addr + off
		p := a / s.Config.PageBytes * s.Config.PageBytes
		if s.pages[p] == nil {
			continue
		}
		if masks[p] == nil {
			masks[p] = map[uint32]bool{}
		}
		masks[p][uint32((a-p)/s.Config.SectorBytes)] = true
	}
	for p, bits := range masks {
		k := [2]uint64{p, uint64(gpu)}
		a := s.observed[k]
		if a == nil {
			a = &Access{Page: p, GPU: gpu}
			s.observed[k] = a
			if s.observedReads == nil {
				s.observedReads = map[[2]uint64]map[uint32]bool{}
				s.observedWrites = map[[2]uint64]map[uint32]bool{}
			}
			s.observedReads[k] = map[uint32]bool{}
			s.observedWrites[k] = map[uint32]bool{}
		}
		if s.Plan != nil {
			s.guard(p, gpu, bits, write)
		}
		if s.Plan != nil {
			s.countOwnership(p, gpu, write)
		}
		if write {
			if s.Plan != nil && s.uses[p].fallback() {
				s.Stats.FallbackWriteTransactions++
			}
			m := s.observedWrites[k]
			for x := range bits {
				m[x] = true
			}

			a.WriteTransactions++
		} else {
			m := s.observedReads[k]
			for x := range bits {
				m[x] = true
			}

			a.ReadTransactions++
			if s.Plan != nil {
				s.countRead(p, gpu, bits)
			}
		}
	}
}
func (s *Session) guard(p uint64, gpu int, bits map[uint32]bool, write bool) {
	a := s.planned(p, gpu)
	k := [2]uint64{p, uint64(gpu)}
	want := s.plannedReads[k]
	if want == nil {
		want = set(a.Reads)
	}
	if write {
		want = s.plannedWrites[k]
		if want == nil {
			want = set(a.Writes)
		}
	}
	for x := range bits {
		if !want[x] {
			panic("replica: observed access outside oracle plan")
		}
	}
	if write && s.mapped[[2]uint64{p, uint64(gpu)}] && s.Config.Policy != "infinity" {
		panic("replica: write attempted into read-only replica")
	}
	if !write && s.mapped[[2]uint64{p, uint64(gpu)}] {
		c := s.pages[p].Copies[gpu]
		for x := range bits {
			if !c.Valid[x] || c.SectorVersion[x] < s.pages[p].LastWrite[x] {
				panic("replica: read attempted into stale subblock")
			}
		}
	}
}
func (s *Session) countRead(p uint64, gpu int, bits map[uint32]bool) {
	key := [2]uint64{p, uint64(gpu)}
	u := s.uses[p]
	if u.fallback() {
		s.Stats.FallbackReadTransactions++
		return
	}
	writer := 0
	for g := range u.Writers {
		writer = g
	}
	if writer != 0 && writer != gpu && gpu != int(s.pages[p].Home.DeviceID) && s.baselineValid[key] {
		clean := true
		for x := range bits {
			if u.Dirty[x] {
				s.Stats.S3ConflictSectors++
				clean = false
			}
		}
		if clean {
			s.Stats.S3EligibleTransactions++
			s.Stats.S3EligibleSectors += uint64(len(bits))
		}
	}
	if !s.mapped[key] {
		return
	}
	if writer != 0 {
		if s.existedAtStart[key] {
			s.Stats.WriteEpochExistingReads++
		} else {
			s.Stats.WriteEpochNewReads++
		}
	}
	c := s.pages[p].Copies[gpu]
	if uint64(len(c.Valid)) < s.Config.PageBytes/s.Config.SectorBytes {
		s.Stats.S2CleanTransactions++
	}
}
