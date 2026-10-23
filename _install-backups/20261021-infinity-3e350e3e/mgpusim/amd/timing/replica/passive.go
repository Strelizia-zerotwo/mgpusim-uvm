package replica

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"sort"
	"strings"

	"github.com/sarchlab/akita/v5/mem"
	"github.com/sarchlab/akita/v5/mem/cache/writeback"
	"github.com/sarchlab/akita/v5/mem/cache/writethroughcache"
	"github.com/sarchlab/akita/v5/mem/memprotocol"
	"github.com/sarchlab/akita/v5/mem/vm"
	"github.com/sarchlab/akita/v5/mem/vm/addresstranslator"
	"github.com/sarchlab/akita/v5/timing"
	"github.com/sarchlab/mgpusim/v5/amd/timing/cp"
)

const idealProtocol = "kernel-internal-ideal-v3"

// Only fixed copy service and bulk data transfer have protocol timing. All
// directory/cache/in-flight correctness operations below are functional and
// execute at the same simulation timestamp. Ordinary GPU execution is timed.
type PassiveStats struct {
	Protocol                   string         `json:"protocol"`
	CostModel                  string         `json:"cost_model"`
	ManagedRequests            uint64         `json:"managed_requests"`
	ReadRequests               uint64         `json:"read_requests"`
	WriteRequests              uint64         `json:"write_requests"`
	WriteTransitions           uint64         `json:"write_transitions"`
	CacheControlPS             uint64         `json:"cache_control_ps_in_kernel"`
	HomeFetchPS                uint64         `json:"home_fetch_ps_in_kernel"`
	MetadataPS                 uint64         `json:"metadata_ps"`
	InflightWaitPS             uint64         `json:"inflight_wait_ps"`
	CopyServicePS              uint64         `json:"copy_service_ps_in_kernel"`
	ChargedFixedPS             uint64         `json:"charged_fixed_ps"`
	ChargedBytes               uint64         `json:"charged_bytes"`
	FunctionalWritebacks       uint64         `json:"functional_home_writebacks"`
	FunctionalInvalidations    uint64         `json:"functional_cache_invalidations"`
	FunctionalReadCorrections  uint64         `json:"functional_read_corrections"`
	StaleReadSectors           uint64         `json:"stale_read_sectors"`
	VersionChecks              uint64         `json:"version_checks"`
	CoalescedReads             uint64         `json:"coalesced_reads"`
	InflightHighWater          uint64         `json:"inflight_high_water"`
	PendingHighWater           uint64         `json:"pending_high_water"`
	ConcurrentRepairsHighWater uint64         `json:"concurrent_repairs_high_water"`
	IntrakernelRefills         uint64         `json:"intrakernel_refills"`
	IntrakernelRepairs         uint64         `json:"intrakernel_repairs"`
	CleanReplicaReads          uint64         `json:"clean_replica_reads_while_page_stale"`
	WriteAfterRepair           uint64         `json:"write_after_repair_metadata_events"`
	FullFallbacks              uint64         `json:"full_page_fallbacks"`
	FallbackReads              uint64         `json:"fallback_home_reads"`
	RepairBytes                uint64         `json:"repair_bytes"`
	RepairEvents               uint64         `json:"repair_events"`
	PeakRetainedFrames         uint64         `json:"peak_retained_frames"`
	PeakStaleRetainedFrames    uint64         `json:"peak_stale_retained_frames"`
	PeakBackendPinnedFrames    uint64         `json:"peak_backend_pinned_frames"`
	Events                     []PassiveEvent `json:"events"`
}
type PassiveEvent struct {
	Kernel       int    `json:"kernel"`
	Kind         string `json:"kind"`
	Page         uint64 `json:"page"`
	GPU          int    `json:"gpu"`
	Version      uint64 `json:"snapshot_version"`
	StartPS      uint64 `json:"start_ps"`
	EndPS        uint64 `json:"end_ps"`
	Bytes        uint64 `json:"bytes"`
	ChargedBytes uint64 `json:"charged_bytes"`
	Full         bool   `json:"full_page"`
}
type passiveLease struct {
	page    vm.Page
	managed bool
	virtual uint64
	write   bool
	offset  uint64
	size    uint64
	data    []byte
}
type idealTransfer struct {
	transmitting bool
	id           uint64
	page         uint64
	gpu          int
	frame        uint64
	copy         *copyState
	version      uint64
	full         bool
	cold         bool
	sectors      []uint32
	data         []byte
	waiters      map[uint64]passiveLease
	start        timing.VTimeInPicoSec
	due          timing.VTimeInPicoSec
	bytes        uint64
}
type idealReadyEvent struct {
	timing.EventBase
	Job uint64
}
type idealCache struct {
	gpu int
	l2  *writeback.Comp
	l1  *writethroughcache.Comp
}

// Passive is an ideal per-request controller. There are no control ports,
// cache-control messages, page locks, GPU repair queues or drain states.
// Reads linearize at their first actual AT permission check. A faulting read
// retains that functional snapshot while its copy service is timed; concurrent
// writes invalidate future reads immediately without waiting for the fault.
type Passive struct {
	s              *Session
	pid            vm.PID
	ats            []*addresstranslator.Comp
	cps            []*cp.Comp
	caches         []idealCache
	initialInput   hash.Hash
	storage        *mem.Storage
	shadow         map[uint64][]byte
	leases         map[uint64]passiveLease
	waiting        map[uint64]*idealTransfer
	transfers      map[uint64]*idealTransfer
	pending        map[[3]uint64]*idealTransfer
	grants         map[uint64]passiveLease
	refs           map[uint64]int
	resident       map[uint64]bool // A newly allocated frame is unreadable until its full copy completes.
	retired        map[uint64]int
	lastCopyKernel map[[2]uint64]int
	repaired       map[uint64]bool
	kernel         int
	now            func() timing.VTimeInPicoSec
	schedule       func(*idealTransfer)
	allocate       func(int) (uint64, bool)
	release        func(int, uint64)
}

func validatePassivePolicy(policy string) {
	switch policy {
	case "inv", "delta", "delta-sub", "infinity":
		return
	}
	panic("ideal kernel-internal supports inv, delta, delta-sub, infinity")
}
func EnablePassive(s *Session) {
	validatePassivePolicy(s.Config.Policy)
	if s.Plan != nil {
		panic("ideal kernel-internal rejects future access plans")
	}
	p := newIdealPassive(s, s.d.Resources().GlobalStorage)
	p.pid = s.ctx.PID()
	p.allocate = s.d.TryAllocateReplicaFrame
	p.release = s.d.ReleaseReplicaFrame
	p.now = s.sim.GetEngine().CurrentTime
	handler := fmt.Sprintf("IdealReplicaReady%d", timing.GetIDGenerator().Generate())
	s.sim.GetEngine().(timing.HandlerRegistrar).RegisterHandler(handler, p)
	p.schedule = func(t *idealTransfer) {
		s.sim.GetEngine().Schedule(idealReadyEvent{timing.MakeEventBase(t.due, handler), t.id})
	}
	for _, component := range s.sim.Components() {
		if at, ok := component.(*addresstranslator.Comp); ok {
			p.ats = append(p.ats, at)
			addresstranslator.SetAccessController(at, p)
		}
		var gpu int
		if _, e := fmt.Sscanf(component.Name(), "GPU[%d]", &gpu); e != nil {
			continue
		}
		if c, ok := component.(*writeback.Comp); ok {
			p.caches = append(p.caches, idealCache{gpu: gpu, l2: c})
		}
		if c, ok := component.(*writethroughcache.Comp); ok && !strings.Contains(c.Name(), "L1I") {
			p.caches = append(p.caches, idealCache{gpu: gpu, l1: c})
		}
		if c, ok := component.(*cp.Comp); ok {
			p.cps = append(p.cps, c)
			cp.SetIdealKernelStart(c, func() {
				for _, cache := range p.caches {
					if cache.gpu == gpu && cache.l1 != nil && !strings.Contains(cache.l1.Name(), "MALL") {
						writethroughcache.FunctionalInvalidateRange(cache.l1, 0, ^uint64(0))
					}
				}
			})
		}
	}
}
func newIdealPassive(s *Session, storage *mem.Storage) *Passive {
	p := &Passive{s: s, storage: storage, initialInput: sha256.New(), shadow: map[uint64][]byte{}, leases: map[uint64]passiveLease{}, waiting: map[uint64]*idealTransfer{}, transfers: map[uint64]*idealTransfer{}, pending: map[[3]uint64]*idealTransfer{}, grants: map[uint64]passiveLease{}, refs: map[uint64]int{}, retired: map[uint64]int{}, lastCopyKernel: map[[2]uint64]int{}, repaired: map[uint64]bool{}, now: func() timing.VTimeInPicoSec { return 0 }}
	p.resident = map[uint64]bool{}
	s.Passive = p
	s.Stats.Passive = &PassiveStats{Protocol: idealProtocol, CostModel: "free immediate coherence; one fault charge plus one bulk bandwidth/latency charge per copy/repair; no extent/header costs"}
	s.Stats.Adapter = "core-" + idealProtocol
	s.Stats.ModelRevision = "replica-" + idealProtocol
	s.Stats.OracleMappingPreinstall = true
	return p
}
func (p *Passive) DeferBlockedAccess() bool { return true }
func (p *Passive) BeginKernel(kernel int)   { p.kernel = kernel } // Label only; no permissions or versions change here.
func (p *Passive) sectors(req memprotocol.AccessReq) map[uint32]bool {
	bits := map[uint32]bool{}
	w, write := req.(memprotocol.WriteReq)
	for off := uint64(0); off < req.GetByteSize(); off++ {
		if write && len(w.DirtyMask) > 0 && !w.DirtyMask[off] {
			continue
		}
		bits[uint32((req.GetAddress()%p.s.Config.PageBytes+off)/p.s.Config.SectorBytes)] = true
	}
	return bits
}
func (p *Passive) pageData(page uint64) []byte {
	if b, ok := p.shadow[page]; ok {
		return b
	}
	p.flushHome(page)
	b, e := p.storage.Read(p.s.pages[page].Home.PAddr, p.s.Config.PageBytes)
	must(e)
	p.shadow[page] = b
	return b
}
func (p *Passive) flushHome(page uint64) {
	st := p.s.pages[page]
	for _, cache := range p.caches {
		if cache.gpu == int(st.Home.DeviceID) && cache.l2 != nil {
			p.s.Stats.Passive.FunctionalWritebacks += writeback.FunctionalWritebackRange(cache.l2, st.Home.PAddr, p.s.Config.PageBytes, p.storage.Write)
		}
	}
}
func (p *Passive) invalidate(gpu int, frame uint64) {
	for _, c := range p.caches {
		if c.gpu != gpu {
			continue
		}
		if c.l2 != nil {
			p.s.Stats.Passive.FunctionalInvalidations += writeback.FunctionalInvalidateRange(c.l2, frame, p.s.Config.PageBytes)
		} else {
			p.s.Stats.Passive.FunctionalInvalidations += writethroughcache.FunctionalInvalidateRange(c.l1, frame, p.s.Config.PageBytes)
		}
	}
}
func (p *Passive) valid(page uint64, c *copyState, bits map[uint32]bool) bool {
	if c == nil || c.Frame == 0 || !c.Had || !p.resident[c.Frame] {
		return false
	}
	st := p.s.pages[page]
	if p.s.Config.Policy == "inv" || p.s.Config.Policy == "delta" {
		return c.Version == st.Version
	}
	for x := range bits {
		if st.LastWrite[x] > c.Version {
			return false
		}
	}
	return true
}
func (p *Passive) Admit(at *addresstranslator.Comp, req memprotocol.AccessReq) bool {
	return p.admit(int(at.Spec().DeviceID), req)
}
func (p *Passive) admit(gpu int, req memprotocol.AccessReq) bool {
	id := req.Meta().ID
	if grant, ok := p.grants[id]; ok {
		delete(p.grants, id)
		delete(p.waiting, id)
		p.leases[id] = grant
		p.count(req, gpu)
		return true
	}
	if p.waiting[id] != nil {
		return false
	}
	page := req.GetAddress() / p.s.Config.PageBytes * p.s.Config.PageBytes
	st := p.s.pages[page]
	if st == nil || st.Retired || req.GetPID() != p.pid {
		p.leases[id] = passiveLease{}
		return true
	}
	if req.GetAddress()+req.GetByteSize() > page+p.s.Config.PageBytes {
		panic("ideal access crosses page")
	}
	bits := p.sectors(req)
	off := req.GetAddress() - page
	l := passiveLease{page: st.Home, managed: true, virtual: page, offset: off, size: req.GetByteSize()}
	if w, ok := req.(memprotocol.WriteReq); ok {
		b := p.pageData(page)
		p.markWrite(page, bits)
		for i, v := range w.Data {
			if len(w.DirtyMask) == 0 || w.DirtyMask[i] {
				b[off+uint64(i)] = v
			}
		}
		// Functional visibility is immediate; the ordinary write still pays its
		// normal cache/RDMA timing. Later physical completion is reconciled below.
		must(p.storage.Write(st.Home.PAddr+off, b[off:off+l.size]))
		l.write = true
		p.leases[id] = l
		p.count(req, gpu)
		return true
	}
	p.s.Stats.Passive.VersionChecks++
	l.data = append([]byte(nil), p.pageData(page)[off:off+l.size]...)
	if gpu == int(st.Home.DeviceID) {
		p.leases[id] = l
		p.count(req, gpu)
		return true
	}
	c := st.Copies[gpu]
	if p.valid(page, c, bits) {
		got, e := p.storage.Read(c.Frame+off, l.size)
		must(e)
		if !bytes.Equal(got, l.data) {
			p.s.Stats.Passive.StaleReadSectors += uint64(len(bits))
			panic("ideal valid replica has stale functional data")
		}
		l.page.PAddr = c.Frame
		l.page.DeviceID = uint64(gpu)
		p.pin(c.Frame)
		p.touch(c)
		p.s.Stats.ReplicaReadTransactions++
		if c.Version != st.Version {
			p.s.Stats.Passive.CleanReplicaReads++
			p.s.Stats.WriteEpochExistingReads++
		}
		p.leases[id] = l
		p.count(req, gpu)
		return true
	}
	key := [3]uint64{page, uint64(gpu), st.Version}
	if t := p.pending[key]; t != nil {
		l.page.PAddr = t.frame
		l.page.DeviceID = uint64(gpu)
		p.pin(t.frame)
		t.waiters[id] = l
		p.waiting[id] = t
		p.s.Stats.Passive.CoalescedReads++
		return false
	}
	if c == nil {
		c = &copyState{Valid: map[uint32]bool{}, SectorVersion: map[uint32]uint64{}}
		st.Copies[gpu] = c
	}
	full := c.Frame == 0 || !c.Had || !p.resident[c.Frame] || p.s.Config.Policy == "inv"
	if c.Frame == 0 && !p.makeFrame(gpu, c) {
		p.s.Stats.Passive.FallbackReads++
		p.leases[id] = l
		p.count(req, gpu)
		return true
	}
	l.page.PAddr = c.Frame
	l.page.DeviceID = uint64(gpu)
	p.pin(c.Frame)
	t := &idealTransfer{id: timing.GetIDGenerator().Generate(), page: page, gpu: gpu, frame: c.Frame, copy: c, version: st.Version, full: full, cold: !c.Had, start: p.now(), waiters: map[uint64]passiveLease{id: l}}
	// Source dirty data and in-flight forwarding are free in this experiment.
	p.flushHome(page)
	must(p.storage.Write(st.Home.PAddr, p.pageData(page)))
	if full {
		t.data = append([]byte(nil), p.pageData(page)...)
		t.bytes = p.s.Config.PageBytes
	} else {
		for x, v := range st.LastWrite {
			if v > c.Version {
				t.sectors = append(t.sectors, x)
			}
		}
		sort.Slice(t.sectors, func(i, j int) bool { return t.sectors[i] < t.sectors[j] })
		for _, x := range t.sectors {
			lo := uint64(x) * p.s.Config.SectorBytes
			t.data = append(t.data, p.pageData(page)[lo:lo+p.s.Config.SectorBytes]...)
		}
		t.bytes = uint64(len(t.data))
	}
	if t.bytes == 0 {
		panic("ideal repair without stale sectors")
	}
	p.waiting[id] = t
	p.transfers[t.id] = t
	p.pending[key] = t
	p.s.Stats.Passive.PendingHighWater = max(p.s.Stats.Passive.PendingHighWater, uint64(len(p.waiting)))
	p.s.Stats.Passive.ConcurrentRepairsHighWater = max(p.s.Stats.Passive.ConcurrentRepairsHighWater, uint64(len(p.transfers)))
	p.snapshot()
	t.due = t.start
	if p.s.Config.Policy == "infinity" {
		p.finishTransfer(t)
		return p.admit(gpu, req)
	}
	t.due += timing.VTimeInPicoSec(p.s.Config.FaultNS) * 1000
	if t.due > t.start {
		p.schedule(t)
	} else {
		p.startData(t)
		if _, ready := p.grants[id]; ready {
			return p.admit(gpu, req)
		}
	}
	return false
}
func (p *Passive) count(req memprotocol.AccessReq, gpu int) {
	st := p.s.pages[req.GetAddress()/p.s.Config.PageBytes*p.s.Config.PageBytes]
	s := p.s.Stats.Passive
	s.ManagedRequests++
	if _, write := req.(memprotocol.WriteReq); write {
		s.WriteRequests++
		if gpu == int(st.Home.DeviceID) {
			p.s.Stats.OwnerWriteTransactions++
		} else {
			p.s.Stats.NonOwnerWriteTransactions++
		}
	} else {
		s.ReadRequests++
		if gpu == int(st.Home.DeviceID) {
			p.s.Stats.OwnerReadTransactions++
		} else {
			p.s.Stats.NonOwnerReadTransactions++
		}
	}
	s.InflightHighWater = max(s.InflightHighWater, uint64(len(p.leases)))
}
func (p *Passive) markWrite(page uint64, bits map[uint32]bool) {
	if len(bits) == 0 {
		return
	}
	st := p.s.pages[page]
	st.Version++
	p.s.Stats.WritePageEpochs++
	for x := range bits {
		st.LastWrite[x] = st.Version
	}
	for gpu, c := range st.Copies {
		if c.Frame == 0 {
			continue
		}
		if c.Readable {
			p.s.Stats.Invalidations++
		}
		c.Readable = false
		if p.s.Config.Policy == "inv" {
			frame := c.Frame
			c.Frame = 0
			p.retire(gpu, frame)
		}
	}
	if p.repaired[page] {
		p.s.Stats.Passive.WriteAfterRepair++
		p.repaired[page] = false
	}
	p.snapshot()
}
func (p *Passive) reserve(gpu, home int, n uint64, start timing.VTimeInPicoSec) timing.VTimeInPicoSec {
	if p.s.Config.Policy == "infinity" {
		return start
	}
	ready := max(p.now(), start+timing.VTimeInPicoSec(p.s.Config.FaultNS)*1000)
	due := p.s.Fabric.Reserve(gpuName(home), gpuName(gpu), n, ready)
	p.s.Stats.Passive.ChargedFixedPS += p.s.Config.FaultNS * 1000
	p.s.Stats.Passive.ChargedBytes += n
	p.s.Stats.Passive.CopyServicePS += uint64(due - start)
	return due
}
func (p *Passive) Handle(event timing.Event) error {
	e := event.(idealReadyEvent)
	t := p.transfers[e.Job]
	if t == nil {
		panic("unknown ideal copy event")
	}
	if t.transmitting {
		p.finishTransfer(t)
	} else {
		p.startData(t)
	}
	return nil
}
func (p *Passive) startData(t *idealTransfer) {
	// Reserve only when fixed service is over. Reserving future bandwidth at
	// fault admission would block unrelated traffic throughout fault_ns.
	t.transmitting = true
	t.due = p.reserve(t.gpu, int(p.s.pages[t.page].Home.DeviceID), t.bytes, t.start)
	if t.due == p.now() {
		p.finishTransfer(t)
	} else {
		p.schedule(t)
	}
}
func (p *Passive) finishTransfer(t *idealTransfer) {
	c := t.copy
	// Writes may invalidate INV's frame while a read is waiting. Such reads
	// retain private snapshots; they never republish an invalidated copy.
	if c.Frame == t.frame && (!c.Had || t.version >= c.Version) {
		if t.full {
			must(p.storage.Write(t.frame, t.data))
		} else {
			for i, x := range t.sectors {
				lo := uint64(i) * p.s.Config.SectorBytes
				must(p.storage.Write(t.frame+uint64(x)*p.s.Config.SectorBytes, t.data[lo:lo+p.s.Config.SectorBytes]))
			}
		}
		c.Had = true
		p.resident[t.frame] = true
		c.Version = t.version
		c.Readable = c.Version == p.s.pages[t.page].Version
		p.invalidate(t.gpu, t.frame)
		p.touch(c)
	}
	st := &p.s.Stats
	st.Faults++
	st.CopyBytes += t.bytes
	key := [2]uint64{t.page, uint64(t.gpu)}
	if t.cold {
		st.ColdCopies++
	} else {
		p.repaired[t.page] = true
		p.s.Stats.Passive.RepairEvents++
		p.s.Stats.Passive.RepairBytes += t.bytes
		if p.s.Config.Policy == "inv" {
			st.Refills++
			if p.lastCopyKernel[key] == p.kernel {
				st.Passive.IntrakernelRefills++
			}
		} else {
			st.Repairs++
			if p.lastCopyKernel[key] == p.kernel {
				st.Passive.IntrakernelRepairs++
			}
		}
		if t.full && p.s.Config.Policy != "inv" {
			st.Passive.FullFallbacks++
		}
	}
	p.lastCopyKernel[key] = p.kernel
	charged := t.bytes
	if p.s.Config.Policy == "infinity" {
		charged = 0
	}
	kind := "repair"
	if t.cold {
		kind = "cold-copy"
	} else if p.s.Config.Policy == "inv" {
		kind = "refill"
	}
	st.Passive.Events = append(st.Passive.Events, PassiveEvent{Kernel: p.kernel, Kind: kind, Page: t.page, GPU: t.gpu, Version: t.version, StartPS: uint64(t.start), EndPS: uint64(t.due), Bytes: t.bytes, ChargedBytes: charged, Full: t.full})
	st.RecoveryEvents = append(st.RecoveryEvents, RecoveryEvent{HomeGPU: int(p.s.pages[t.page].Home.DeviceID), Role: "runtime", RepairKind: p.s.Config.Policy, Epoch: p.kernel, Page: t.page, GPU: t.gpu, Version: t.version, Cold: t.cold, Bytes: t.bytes, ChargedBytes: charged, StartPS: uint64(t.start), DuePS: uint64(t.due)})
	for id, l := range t.waiters {
		p.grants[id] = l
	}
	delete(p.transfers, t.id)
	delete(p.pending, [3]uint64{t.page, uint64(t.gpu), t.version})
	p.snapshot()
	for _, at := range p.ats {
		at.TickLater()
	}
}
func (p *Passive) Resolve(_ *addresstranslator.Comp, id uint64, page vm.Page) vm.Page {
	l, ok := p.leases[id]
	if !ok {
		panic("ideal access has no snapshot lease")
	}
	if l.managed {
		return l.page
	}
	return page
}
func (p *Passive) ReadData(_ *addresstranslator.Comp, id uint64, physical []byte) []byte {
	l := p.leases[id]
	if !l.managed {
		return physical
	}
	if l.write || uint64(len(l.data)) != l.size {
		panic("invalid ideal read snapshot")
	}
	if !bytes.Equal(l.data, physical) {
		p.s.Stats.Passive.FunctionalReadCorrections++
	}
	return append([]byte(nil), l.data...)
}
func (p *Passive) Complete(_ *addresstranslator.Comp, id uint64) {
	l, ok := p.leases[id]
	if !ok {
		panic("ideal completion without lease")
	}
	if l.managed {
		if l.write {
			b := p.pageData(l.virtual)
			must(p.storage.Write(p.s.pages[l.virtual].Home.PAddr+l.offset, b[l.offset:l.offset+l.size]))
		} else if l.page.PAddr != p.s.pages[l.virtual].Home.PAddr {
			p.unpin(l.page.PAddr)
		}
	}
	delete(p.leases, id)
}
func (p *Passive) pin(frame uint64) { p.refs[frame]++ }
func (p *Passive) unpin(frame uint64) {
	p.refs[frame]--
	if p.refs[frame] < 0 {
		panic("negative ideal frame references")
	}
	if p.refs[frame] == 0 {
		delete(p.refs, frame)
		if gpu, ok := p.retired[frame]; ok {
			delete(p.retired, frame)
			p.release(gpu, frame)
		}
	}
}
func (p *Passive) retire(gpu int, frame uint64) {
	delete(p.resident, frame)
	p.invalidate(gpu, frame)
	if p.refs[frame] > 0 {
		p.retired[frame] = gpu
		p.s.Stats.Passive.PeakBackendPinnedFrames = max(p.s.Stats.Passive.PeakBackendPinnedFrames, uint64(len(p.retired)))
	} else {
		p.release(gpu, frame)
	}
}
func (p *Passive) touch(c *copyState) { p.s.clock++; c.Stamp = p.s.clock }
func (p *Passive) makeFrame(gpu int, c *copyState) bool {
	limit := p.s.Config.ReplicaPagesPerGPU
	count := 0
	var victim *copyState
	for _, st := range p.s.pages {
		if v := st.Copies[gpu]; v != nil && v.Frame != 0 {
			count++
			if victim == nil || v.Stamp < victim.Stamp || (v.Stamp == victim.Stamp && v.Frame < victim.Frame) {
				victim = v
			}
		}
	}
	if limit > 0 && count >= limit {
		frame := victim.Frame
		victim.Frame = 0
		p.retire(gpu, frame)
		p.s.Stats.Evictions++
	}
	frame, ok := p.allocate(gpu)
	if !ok {
		return false
	}
	c.Frame = frame
	delete(p.resident, frame)
	p.invalidate(gpu, frame)
	return true
}
func (p *Passive) snapshot() {
	var frames, stale uint64
	p.s.Stats.VersionedPages = 0
	for _, st := range p.s.pages {
		if st.Version > 0 {
			p.s.Stats.VersionedPages++
		}
		for _, c := range st.Copies {
			if c.Frame != 0 {
				frames++
				if c.Had && c.Version != st.Version {
					stale++
				}
			}
		}
	}
	p.s.Stats.PeakReplicaPages = max(p.s.Stats.PeakReplicaPages, frames)
	p.s.Stats.PeakStalePages = max(p.s.Stats.PeakStalePages, stale)
	p.s.Stats.RetainedPages = frames
	if p.s.Config.Policy != "inv" {
		p.s.Stats.Passive.PeakRetainedFrames = max(p.s.Stats.Passive.PeakRetainedFrames, frames)
		p.s.Stats.Passive.PeakStaleRetainedFrames = max(p.s.Stats.Passive.PeakStaleRetainedFrames, stale)
	}
}
func (p *Passive) CheckAtBarrier() {
	// This only materializes functional results for host verification. Copy
	// decisions and versions remain driven by individual in-kernel requests.
	if len(p.waiting) > 0 || len(p.leases) > 0 || len(p.transfers) > 0 {
		panic("unfinished ideal request")
	}
	for _, cache := range p.caches {
		if cache.l2 != nil {
			p.s.Stats.Passive.FunctionalWritebacks += writeback.FunctionalWritebackRange(cache.l2, 0, ^uint64(0), p.storage.Write)
		}
	}
	for page, b := range p.shadow {
		st := p.s.pages[page]
		if !st.Retired {
			must(p.storage.Write(st.Home.PAddr, b))
			// MI300X host DMA reads through its memory-side MALL. The ideal
			// publish bypasses it, so discard its old clean lines for free.
			for _, cache := range p.caches {
				if cache.gpu == int(st.Home.DeviceID) && cache.l1 != nil && strings.Contains(cache.l1.Name(), ".MALL[") {
					p.s.Stats.Passive.FunctionalInvalidations += writethroughcache.FunctionalInvalidateRange(cache.l1, st.Home.PAddr, p.s.Config.PageBytes)
				}
			}
		}
	}
	p.snapshot()
}
func (p *Passive) HostWrite(page uint64, bits map[uint32]bool) {
	p.markWrite(page, bits)
	delete(p.shadow, page)
	frame := p.s.pages[page].Home.PAddr
	for _, c := range p.caches {
		if c.l2 != nil {
			writeback.FunctionalInvalidateRange(c.l2, frame, p.s.Config.PageBytes)
		} else {
			writethroughcache.FunctionalInvalidateRange(c.l1, frame, p.s.Config.PageBytes)
		}
	}
}
func (p *Passive) InitialWrite(ptr uint64, src interface{}) {
	must(binary.Write(p.initialInput, binary.LittleEndian, ptr))
	must(binary.Write(p.initialInput, binary.LittleEndian, int64(binary.Size(src))))
	must(binary.Write(p.initialInput, binary.LittleEndian, src))
}
func (p *Passive) Finish() {
	p.s.Stats.InitialInputSHA256 = fmt.Sprintf("%x", p.initialInput.Sum(nil))
	if len(p.waiting) > 0 || len(p.leases) > 0 || len(p.transfers) > 0 || len(p.grants) > 0 || len(p.refs) > 0 || len(p.retired) > 0 {
		panic("ideal protocol did not finish")
	}
	for _, at := range p.ats {
		addresstranslator.SetAccessController(at, nil)
	}
	for _, c := range p.cps {
		cp.SetIdealKernelStart(c, nil)
	}
}
