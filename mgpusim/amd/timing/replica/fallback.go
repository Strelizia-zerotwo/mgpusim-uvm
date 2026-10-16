package replica

import "sort"

// PageUse describes all accesses to one page during an explicit global phase.
// Dirty is the union across writers, including pages that fall back to home.
type PageUse struct {
	Writers           map[int]bool
	Dirty             map[uint32]bool
	MultiWriter       bool
	ReadWriteConflict bool
}

func (u *PageUse) fallback() bool { return u.MultiWriter || u.ReadWriteConflict }
func classify(e Epoch) map[uint64]*PageUse {
	out := map[uint64]*PageUse{}
	for _, a := range e.Accesses {
		u := out[a.Page]
		if u == nil {
			u = &PageUse{Writers: map[int]bool{}, Dirty: map[uint32]bool{}}
			out[a.Page] = u
		}
		if len(a.Writes) > 0 {
			u.Writers[a.GPU] = true
		}
		for _, x := range a.Writes {
			u.Dirty[x] = true
		}
	}
	for _, a := range e.Accesses {
		u := out[a.Page]
		u.MultiWriter = len(u.Writers) > 1
		for _, other := range e.Accesses {
			if other.Page != a.Page || other.GPU == a.GPU || len(other.Writes) == 0 {
				continue
			}
			writes := set(other.Writes)
			for _, x := range a.Reads {
				if writes[x] {
					u.ReadWriteConflict = true
				}
			}
		}
	}
	return out
}

// FallbackEvent reports one page/epoch for which all GPUs use the authoritative
// frame. It says nothing about whether the original program has a data race.
type FallbackEvent struct {
	Epoch             int    `json:"epoch"`
	Page              uint64 `json:"page"`
	MultiWriter       bool   `json:"multiple_writers"`
	ReadWriteConflict bool   `json:"read_write_overlap"`
}

func (s *Session) countFallbacks() {
	for _, page := range s.sortedPages() {
		u := s.uses[page]
		if !u.fallback() {
			continue
		}
		s.Stats.FallbackPageEpochs++
		if u.MultiWriter {
			s.Stats.MultiWriterPageEpochs++
		}
		if u.ReadWriteConflict {
			s.Stats.ReadWriteConflictPageEpochs++
		}
		s.Stats.FallbackEvents = append(s.Stats.FallbackEvents, FallbackEvent{
			Epoch: s.at, Page: page, MultiWriter: u.MultiWriter, ReadWriteConflict: u.ReadWriteConflict})
	}
	for _, a := range s.active.Accesses {
		if s.uses[a.Page].fallback() {
			s.Stats.FallbackGPUPageAccesses++
		}
	}
}

func (s *Session) sortedPages() []uint64 {
	out := make([]uint64, 0, len(s.uses))
	for p := range s.uses {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
