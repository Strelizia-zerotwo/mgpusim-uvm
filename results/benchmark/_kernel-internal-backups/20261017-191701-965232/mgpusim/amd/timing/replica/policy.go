package replica

import (
	"github.com/sarchlab/akita/v5/timing"
	"sort"
)

func (s *Session) writers(e Epoch) map[uint64]int {
	m := map[uint64]int{}
	for _, a := range e.Accesses {
		if len(a.Writes) > 0 {
			if old := m[a.Page]; old != 0 && old != a.GPU {
				m[a.Page] = -1
			} else {
				m[a.Page] = a.GPU
			}
		}
	}
	return m
}
func (s *Session) prepare() {
	e := *s.active
	ws := s.writers(e)
	s.uses = classify(e)
	s.countFallbacks()
	s.invalidateTLBs()
	s.Stats.TLBFlushEpochs++
	s.prepareShadow(e, ws)
	s.invalidateBeforeWrite(ws)
	// Stable order is essential: capacity evictions and link arbitration must not
	// depend on Go map iteration order.
	accesses := append([]Access(nil), e.Accesses...)
	sort.Slice(accesses, func(i, j int) bool {
		a, b := accesses[i], accesses[j]
		if a.Page != b.Page {
			return a.Page < b.Page
		}
		return a.GPU < b.GPU
	})
	now := s.sim.GetEngine().CurrentTime()
	due := now
	slots := make([]timing.VTimeInPicoSec, s.Config.FaultSlots)
	pinned := map[[2]uint64]bool{}
	for _, a := range accesses {
		pinned[[2]uint64{a.Page, uint64(a.GPU)}] = true
	}
	firstEvent := len(s.Stats.RecoveryEvents)
	for _, a := range accesses {
		due = max(due, s.prepareAccess(a, ws, pinned, slots, now))
	}
	if s.Config.Policy == "i2" {
		var sectors uint64
		for _, a := range accesses {
			sectors += uint64(len(a.Writes))
		}
		due += timing.VTimeInPicoSec(sectors*s.Config.MetadataNS) * 1000
	}
	if due > now {
		s.chargeRecoveryRole(firstEvent, uint64(due-now))
		s.Stats.RecoveryWaitPS += uint64(due - now)
		s.d.AdvanceAtBarrier(due - now)
	}
	s.snapshot()
}

func (s *Session) prepareAccess(a Access, ws map[uint64]int, pinned map[[2]uint64]bool,
	slots []timing.VTimeInPicoSec, now timing.VTimeInPicoSec) timing.VTimeInPicoSec {
	st, ok := s.pages[a.Page]
	if !ok {
		panic("replica: plan names unregistered page")
	}
	key := [2]uint64{a.Page, uint64(a.GPU)}
	local := s.canReplicate(st, a, ws[a.Page] != 0)
	mapped := st.Home
	due := now
	if local {
		c := st.Copies[a.GPU]
		if c == nil {
			c = &copyState{Valid: map[uint32]bool{}, SectorVersion: map[uint32]uint64{}}
			st.Copies[a.GPU] = c
		}
		if c.Frame == 0 && s.room(a.GPU, pinned) {
			c.Frame = s.d.AllocateReplicaFrame(a.GPU)
		}
		if c.Frame != 0 {
			need := s.required(c, a)
			if len(need) > 0 {
				if ws[a.Page] != 0 && !s.existedAtStart[key] {
					s.Stats.WriteEpochNewCopies++
					if s.Config.Policy != "infinity" {
						s.Stats.WriteEpochNewCopyBytes += uint64(len(need)) * s.Config.SectorBytes
						s.Stats.WriteEpochNewCopyFaults++
					}
				}
				due = s.copyData(st, c, a.GPU, need, slots, now)
			}
			s.clock++
			c.Stamp = s.clock
			mapped.PAddr = c.Frame
			mapped.DeviceID = uint64(a.GPU)
			s.mapped[key] = true
		}
	}
	s.tables.View(a.GPU - 1).InstallAtBarrier(mapped)
	s.Stats.MappingInstalls++
	return due
}
func (s *Session) required(c *copyState, a Access) map[uint32]bool {
	need := map[uint32]bool{}
	if !c.Had || (s.Config.Policy == "inv" && len(c.Valid) == 0) {
		for x := uint32(0); uint64(x) < s.Config.PageBytes/s.Config.SectorBytes; x++ {
			need[x] = true
		}
	} else {
		for _, x := range a.Reads {
			if !c.Valid[x] {
				need[x] = true
			}
		}
	}
	if s.Config.Policy == "retain" && !c.Readable {
		for x := uint32(0); uint64(x) < s.Config.PageBytes/s.Config.SectorBytes; x++ {
			if !c.Valid[x] {
				need[x] = true
			}
		}
	}
	if len(need) > 0 && c.Had {
		switch s.Config.Policy {
		case "i1-full-stale":
			need = s.allStale(c)
		case "i1-adaptive":
			if s.preferFullStale(c, a, need) {
				need = s.allStale(c)
			}
		}
	}
	return s.expandRepair(need)
}
func (s *Session) expandRepair(need map[uint32]bool) map[uint32]bool {
	if s.Config.TransferGranBytes > s.Config.SectorBytes {
		expanded := map[uint32]bool{}
		n := uint32(s.Config.TransferGranBytes / s.Config.SectorBytes)
		for x := range need {
			base := x / n * n
			for i := uint32(0); i < n; i++ {
				expanded[base+i] = true
			}
		}
		need = expanded
	}
	return need
}
func (s *Session) copyData(st *pageState, c *copyState, gpu int, need map[uint32]bool,
	slots []timing.VTimeInPicoSec, now timing.VTimeInPicoSec) timing.VTimeInPicoSec {
	due := now
	event := RecoveryEvent{HomeGPU: int(st.Home.DeviceID), Role: s.pageOwnership(st.Home.VAddr).Role,
		RepairKind: s.Config.Policy, Epoch: s.at, Page: st.Home.VAddr, GPU: gpu, Version: st.Version, Cold: !c.Had,
		Bytes: uint64(len(need)) * s.Config.SectorBytes, StartPS: uint64(now), Layouts: map[uint64]Granularity{}}
	for _, g := range []uint64{32, 128, 512, 4096} {
		if g >= s.Config.SectorBytes && g <= s.Config.PageBytes {
			event.Layouts[g] = intervals(need, s.Config.SectorBytes, g)
		}
	}
	if s.Config.Policy != "infinity" {
		s.Stats.Faults++
		if !c.Had {
			s.Stats.ColdCopies++
		} else if s.Config.Policy == "inv" {
			s.Stats.Refills++
		} else {
			s.Stats.Repairs++
		}
		n := uint64(len(need)) * s.Config.SectorBytes
		s.Stats.CopyBytes += n
		event.ChargedBytes = n
		idx := 0
		for i := range slots {
			if slots[i] < slots[idx] {
				idx = i
			}
		}
		ready := max(now, slots[idx]) + timing.VTimeInPicoSec(s.Config.FaultNS)*1000
		slots[idx] = ready
		xs := sorted(need)
		for lo := 0; lo < len(xs); {
			hi := lo + 1
			for hi < len(xs) && xs[hi] == xs[hi-1]+1 {
				hi++
			}
			bytes := uint64(hi-lo) * s.Config.SectorBytes
			due = max(due, s.Fabric.Reserve(gpuName(int(st.Home.DeviceID)), gpuName(gpu), bytes+16, ready))
			lo = hi
		}
		if c.Had {
			s.countGranularities(need)
		}
	}
	for _, x := range sorted(need) {
		off := uint64(x) * s.Config.SectorBytes
		b, err := s.d.Resources().GlobalStorage.Read(st.Home.PAddr+off, s.Config.SectorBytes)
		must(err)
		must(s.d.Resources().GlobalStorage.Write(c.Frame+off, b))
		c.Valid[x] = true
		c.SectorVersion[x] = st.Version
	}
	event.DuePS = uint64(due)
	s.Stats.RecoveryEvents = append(s.Stats.RecoveryEvents, event)
	c.Had = true
	c.Readable = true
	return due
}
func (s *Session) countGranularities(need map[uint32]bool) {
	for _, g := range []uint64{32, 128, 512, 4096} {
		if g > s.Config.PageBytes || g < s.Config.SectorBytes {
			continue
		}
		v := s.Stats.Granularities[g]
		x := intervals(need, s.Config.SectorBytes, g)
		v.Bytes += x.Bytes
		v.Blocks += x.Blocks
		v.Extents += x.Extents
		s.Stats.Granularities[g] = v
	}
}

func (s *Session) release(g int, c *copyState) {
	s.d.ReleaseReplicaFrame(g, c.Frame)
	c.Frame = 0
	c.Valid = map[uint32]bool{}
}
func (s *Session) room(gpu int, pinned map[[2]uint64]bool) bool {
	if s.Config.ReplicaPagesPerGPU == 0 {
		return true
	}
	count := 0
	var oldest *copyState
	for p, st := range s.pages {
		if c := st.Copies[gpu]; c != nil && c.Frame != 0 {
			count++
			older := oldest == nil || c.Stamp < oldest.Stamp ||
				(c.Stamp == oldest.Stamp && c.Frame < oldest.Frame)
			if !pinned[[2]uint64{p, uint64(gpu)}] && older {
				oldest = c
			}
		}
	}
	if count < s.Config.ReplicaPagesPerGPU {
		return true
	}
	if oldest == nil {
		return false
	}
	s.release(gpu, oldest)
	oldest.Had = false
	s.Stats.Evictions++
	return true
}
func (s *Session) commit(e Epoch) {
	var inv uint64
	for _, page := range s.sortedPages() {
		u := s.uses[page]
		if len(u.Dirty) == 0 {
			continue
		}
		st := s.pages[page]
		writer := 0
		if len(u.Writers) == 1 {
			for gpu := range u.Writers {
				writer = gpu
			}
		}
		a := Access{Page: page, GPU: writer, Writes: sorted(u.Dirty)}
		s.gatherInfinity(st, a)
		st.Version++
		s.Stats.WritePageEpochs++
		for x := range u.Dirty {
			st.LastWrite[x] = st.Version
		}
		inv += s.invalidateAfterWrite(st, a)
	}
	if s.Config.Policy == "i2" && inv > 0 {
		d := timing.VTimeInPicoSec(inv*s.Config.ShootdownNS) * 1000
		s.Stats.RecoveryWaitPS += uint64(d)
		s.d.AdvanceAtBarrier(d)
	}
	s.snapshot()
}

// canReplicate makes the S3 treatment explicit. Main i1 permits only an
// already-resident, currently readable copy in a write epoch; write-prefetch
// is a separate intervention with separately counted bytes, faults and reads.
func (s *Session) canReplicate(st *pageState, a Access, writing bool) bool {
	if s.Config.Policy == "norep" || a.GPU == int(st.Home.DeviceID) || s.uses[a.Page].fallback() {
		return false
	}
	if len(a.Writes) > 0 {
		return s.Config.Policy == "infinity"
	}
	if len(a.Reads) == 0 {
		return false
	}
	if !writing {
		return true
	}
	switch s.Config.Policy {
	case "inv", "retain", "i1-no-s3":
		return false
	case "infinity":
		return true
	case "i1-write-prefetch":
		if !s.existedAtStart[[2]uint64{a.Page, uint64(a.GPU)}] {
			return true
		}
	}
	key := [2]uint64{a.Page, uint64(a.GPU)}
	c := st.Copies[a.GPU]
	if !s.existedAtStart[key] || c == nil || c.Frame == 0 || !c.Had {
		return false
	}
	for _, x := range a.Reads {
		if !c.Valid[x] || c.SectorVersion[x] < st.LastWrite[x] {
			return false
		}
	}
	return true
}

func (s *Session) snapshot() {
	var pages, stale uint64
	s.Stats.VersionedPages = 0
	for _, p := range s.pages {
		if p.Version > 0 {
			s.Stats.VersionedPages++
		}
		for _, c := range p.Copies {
			if c.Frame != 0 {
				pages++
				if uint64(len(c.Valid)) < s.Config.PageBytes/s.Config.SectorBytes {
					stale++
				}
			}
		}
	}
	s.Stats.PeakReplicaPages = max(s.Stats.PeakReplicaPages, pages)
	s.Stats.PeakStalePages = max(s.Stats.PeakStalePages, stale)
	s.Stats.RetainedPages = pages
}

func (s *Session) gatherInfinity(st *pageState, a Access) {
	if s.Config.Policy == "infinity" && s.mapped[[2]uint64{a.Page, uint64(a.GPU)}] {
		writer := st.Copies[a.GPU]
		for _, x := range a.Writes {
			off := uint64(x) * s.Config.SectorBytes
			bytes, err := s.d.Resources().GlobalStorage.Read(writer.Frame+off, s.Config.SectorBytes)
			must(err)
			must(s.d.Resources().GlobalStorage.Write(st.Home.PAddr+off, bytes))
		}
	}
}

func (s *Session) invalidateBeforeWrite(ws map[uint64]int) {
	if s.Config.Policy == "inv" || s.Config.Policy == "retain" {
		for p := range ws {
			for g, c := range s.pages[p].Copies {
				if c.Frame != 0 && (s.Config.Policy == "inv" || c.Readable) {
					s.Stats.Invalidations++
					if s.Config.Policy == "inv" {
						s.release(g, c)
					} else {
						c.Readable = false
					}
				}
			}
		}
	}
}

func (s *Session) invalidateAfterWrite(st *pageState, a Access) uint64 {
	var count uint64
	for gpu, c := range st.Copies {
		if s.Config.Policy == "infinity" && gpu == a.GPU {
			for _, x := range a.Writes {
				c.Valid[x] = true
				c.SectorVersion[x] = st.Version
			}
			continue
		}
		if c.Frame == 0 {
			continue
		}
		changed := false
		for _, x := range a.Writes {
			if c.Valid[x] {
				changed = true
			}
			delete(c.Valid, x)
		}
		if changed {
			count++
			if s.Config.Policy != "retain" {
				s.Stats.Invalidations++
			}
		}
	}
	return count
}

// prepareShadow replays INV's capacity as well as validity. Without this a
// frame evicted for pressure could incorrectly be counted as a preexisting
// replica in S3, even though its old PTE was no longer usable.
func (s *Session) prepareShadow(e Epoch, ws map[uint64]int) {
	if s.shadowStamp == nil {
		s.shadowStamp = map[[2]uint64]uint64{}
	}
	for k, v := range s.shadow {
		s.baselineValid[k] = v
		if v && ws[k[0]] != 0 {
			s.Stats.S4Targets++
			s.shadow[k] = false
		}
	}
	xs := append([]Access(nil), e.Accesses...)
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].Page != xs[j].Page {
			return xs[i].Page < xs[j].Page
		}
		return xs[i].GPU < xs[j].GPU
	})
	pinned := map[[2]uint64]bool{}
	for _, a := range xs {
		pinned[[2]uint64{a.Page, uint64(a.GPU)}] = true
	}
	for _, a := range xs {
		if s.pages[a.Page] == nil {
			panic("replica: plan names unregistered page")
		}
		if ws[a.Page] != 0 || len(a.Reads) == 0 || uint64(a.GPU) == s.pages[a.Page].Home.DeviceID {
			continue
		}
		key := [2]uint64{a.Page, uint64(a.GPU)}
		if !s.shadow[key] && !s.shadowRoom(key, pinned) {
			continue
		}
		s.shadow[key] = true
		s.shadowClock++
		s.shadowStamp[key] = s.shadowClock
	}
}
func (s *Session) shadowRoom(key [2]uint64, pinned map[[2]uint64]bool) bool {
	if s.Config.ReplicaPagesPerGPU == 0 {
		return true
	}
	count := 0
	var victim [2]uint64
	found := false
	for k, v := range s.shadow {
		if !v || k[1] != key[1] {
			continue
		}
		count++
		if pinned[k] {
			continue
		}
		if !found || s.shadowStamp[k] < s.shadowStamp[victim] ||
			(s.shadowStamp[k] == s.shadowStamp[victim] && k[0] < victim[0]) {
			victim = k
			found = true
		}
	}
	if count < s.Config.ReplicaPagesPerGPU {
		return true
	}
	if !found {
		return false
	}
	s.shadow[victim] = false
	return true
}
