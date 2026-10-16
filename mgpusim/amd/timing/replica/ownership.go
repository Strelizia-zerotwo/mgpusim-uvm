package replica

import "sort"

// PageOwnership describes the authority actually used by the MMUs, plus the
// full plan's readers and writers. Roles describe placement, not an algorithm.
type PageOwnership struct {
	Page    uint64 `json:"page"`
	HomeGPU int    `json:"home_gpu"`
	Readers []int  `json:"readers"`
	Writers []int  `json:"writers"`
	Role    string `json:"role"`
}

func (s *Session) pageOwnership(page uint64) PageOwnership {
	out := PageOwnership{Page: page, HomeGPU: int(s.pages[page].Home.DeviceID), Role: "read-only"}
	p := s.Plan
	if p == nil {
		p = s.Recorded
	}
	readers, writers := map[int]bool{}, map[int]bool{}
	for _, e := range p.Epochs {
		for _, a := range e.Accesses {
			if a.Page != page {
				continue
			}
			if len(a.Reads) > 0 {
				readers[a.GPU] = true
			}
			if len(a.Writes) > 0 {
				writers[a.GPU] = true
			}
		}
	}
	for g := range readers {
		out.Readers = append(out.Readers, g)
	}
	for g := range writers {
		out.Writers = append(out.Writers, g)
	}
	sort.Ints(out.Readers)
	sort.Ints(out.Writers)
	if len(writers) > 0 {
		out.Role = "owner-private"
		for g := range writers {
			if g != out.HomeGPU {
				out.Role = "non-owner-writer"
				return out
			}
		}
		for g := range readers {
			if g != out.HomeGPU {
				out.Role = "owner-written-shared-read"
			}
		}
	}
	return out
}
func (s *Session) countOwnership(page uint64, gpu int, write bool) {
	owner := gpu == int(s.pages[page].Home.DeviceID)
	if write {
		if owner {
			s.Stats.OwnerWriteTransactions++
		} else {
			s.Stats.NonOwnerWriteTransactions++
		}
		return
	}
	if owner {
		s.Stats.OwnerReadTransactions++
	} else {
		s.Stats.NonOwnerReadTransactions++
	}
	if s.mapped[[2]uint64{page, uint64(gpu)}] {
		s.Stats.ReplicaReadTransactions++
	} else if !owner {
		s.Stats.RemoteHomeReadTransactions++
	}
}
func (s *Session) chargeRecoveryRole(first int, wait uint64) {
	if s.Stats.RecoveryWaitByRole == nil {
		s.Stats.RecoveryWaitByRole = map[string]uint64{}
	}
	role := ""
	for _, e := range s.Stats.RecoveryEvents[first:] {
		if role == "" {
			role = e.Role
		} else if role != e.Role {
			role = "mixed"
			break
		}
	}
	if role == "" {
		role = "metadata"
	}
	s.Stats.RecoveryWaitByRole[role] += wait
}
func (s *Session) finishOwnership() {
	s.Stats.HomePages = map[int]uint64{}
	pages := make([]uint64, 0, len(s.pages))
	for page := range s.pages {
		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i] < pages[j] })
	for _, page := range pages {
		x := s.pageOwnership(page)
		s.Stats.Ownership = append(s.Stats.Ownership, x)
		s.Stats.HomePages[x.HomeGPU]++
	}
	s.Stats.CopyBytesByRole = map[string]uint64{}
	s.Stats.CopyFaultsByRole = map[string]uint64{}
	for _, e := range s.Stats.RecoveryEvents {
		s.Stats.CopyBytesByRole[e.Role] += e.ChargedBytes
		if s.Config.Policy != "infinity" {
			s.Stats.CopyFaultsByRole[e.Role]++
		}
	}
}
