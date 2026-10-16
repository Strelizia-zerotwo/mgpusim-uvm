package replica

import "math"

func (s *Session) allStale(c *copyState) map[uint32]bool {
	m := map[uint32]bool{}
	for x := uint32(0); uint64(x) < s.Config.PageBytes/s.Config.SectorBytes; x++ {
		if !c.Valid[x] {
			m[x] = true
		}
	}
	return m
}

// preferFullStale compares isolated service costs up to the next write of this
// page, using the oracle plan. This is a per-copy lookahead heuristic, NOT an
// end-to-end upper bound: shared-link arbitration and later epochs can differ.
func (s *Session) preferFullStale(c *copyState, a Access, demand map[uint32]bool) bool {
	full := s.expandRepair(s.allStale(c))
	lazy := s.expandRepair(demand)
	lazyCost := s.repairServicePS(lazy)
	valid := set(sorted(c.Valid))
	for x := range lazy {
		valid[x] = true
	}
	for _, v := range s.readsUntilNextWrite(a) {
		need := map[uint32]bool{}
		for _, x := range v.Reads {
			if !valid[x] {
				need[x] = true
			}
		}
		need = s.expandRepair(need)
		lazyCost += s.repairServicePS(need)
		for x := range need {
			valid[x] = true
		}
	}
	return s.repairServicePS(full) < lazyCost
}
func (s *Session) repairServicePS(need map[uint32]bool) float64 {
	if len(need) == 0 {
		return 0
	}
	layout := intervals(need, s.Config.SectorBytes, s.Config.SectorBytes)
	cost := float64(s.Config.FaultNS+s.Config.LinkNS) * 1000
	if s.Config.BandwidthGBs > 0 {
		cost += math.Ceil(float64(layout.Bytes+16*layout.Extents) * 1000 / s.Config.BandwidthGBs)
	}
	return cost
}

func (s *Session) readsUntilNextWrite(a Access) []Access {
	var reads []Access
	if s.Plan == nil {
		return reads
	}
	for _, e := range s.Plan.Epochs[s.at+1:] {
		for _, v := range e.Accesses {
			if v.Page == a.Page && len(v.Writes) > 0 {
				return reads
			}
		}
		for _, v := range e.Accesses {
			if v.Page == a.Page && v.GPU == a.GPU {
				reads = append(reads, v)
			}
		}
	}
	return reads
}
