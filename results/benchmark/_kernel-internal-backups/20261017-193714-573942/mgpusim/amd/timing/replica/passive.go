package replica

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"sort"
	"strings"

	"github.com/sarchlab/akita/v5/mem/cache/writeback"
	"github.com/sarchlab/akita/v5/mem/cache/writethroughcache"
	"github.com/sarchlab/akita/v5/mem/memcontrolprotocol"
	"github.com/sarchlab/akita/v5/mem/memprotocol"
	"github.com/sarchlab/akita/v5/mem/vm"
	"github.com/sarchlab/akita/v5/mem/vm/addresstranslator"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/modeling"
	"github.com/sarchlab/akita/v5/timing"
	"github.com/sarchlab/mgpusim/v5/amd/timing/cp"
	"github.com/sarchlab/mgpusim/v5/amd/timing/rdma"
)

// Durations below are sums of job durations, included in kernel_ps. Concurrent
// jobs overlap; neither these sums nor recovery_wait_ps may be added to time.
type PassiveStats struct {
	Protocol                     string                 `json:"protocol"`
	AdaptiveRule                 string                 `json:"adaptive_rule"`
	ManagedRequests              uint64                 `json:"managed_requests"`
	ReadRequests                 uint64                 `json:"read_requests"`
	WriteRequests                uint64                 `json:"write_requests"`
	Transitions                  uint64                 `json:"transitions"`
	WriteTransitions             uint64                 `json:"write_transitions"`
	CacheControlPS               uint64                 `json:"cache_control_ps_in_kernel"`
	CopyServicePS                uint64                 `json:"copy_service_ps_in_kernel"`
	HomeFetchPS                  uint64                 `json:"home_fetch_ps_in_kernel"`
	HomeFetchRequests            uint64                 `json:"home_fetch_requests"`
	HomeFetchBytes               uint64                 `json:"home_fetch_bytes"`
	HomeFlushEvents              uint64                 `json:"home_flush_events"`
	ReplicaInvalidateEvents      uint64                 `json:"replica_invalidate_events"`
	ReplicaInvalidateBytes       uint64                 `json:"replica_invalidate_bytes"`
	HomeL1InvalidateEvents       uint64                 `json:"home_l1_invalidate_events"`
	StaleReadSectors             uint64                 `json:"stale_read_sectors"`
	VersionChecks                uint64                 `json:"version_checks"`
	FallbackReads                uint64                 `json:"fallback_home_reads"`
	InflightHighWater            uint64                 `json:"inflight_high_water"`
	PendingHighWater             uint64                 `json:"pending_high_water"`
	ConcurrentRepairsHighWater   uint64                 `json:"concurrent_repairs_high_water"`
	RepairsByTrigger             map[string]uint64      `json:"repairs_by_trigger"`
	RepairBytes                  uint64                 `json:"repair_bytes"`
	RepairEvents                 uint64                 `json:"repair_events"`
	RepairGranularities          map[uint64]Granularity `json:"repair_granularities"`
	IntrakernelRefills           uint64                 `json:"intrakernel_refills"`
	IntrakernelRepairs           uint64                 `json:"intrakernel_repairs"`
	WriteAfterRepair             uint64                 `json:"write_after_repair_invalidations"`
	CleanReplicaReadsDuringWrite uint64                 `json:"clean_replica_reads_during_write"`
	DemandRepairs                uint64                 `json:"adaptive_demand_repairs"`
	FullStaleRepairs             uint64                 `json:"adaptive_full_stale_repairs"`
	ExtraRepairBytes             uint64                 `json:"adaptive_extra_repair_bytes"`
	Events                       []PassiveEvent         `json:"events"`
}
type PassiveEvent struct {
	Kernel  int    `json:"kernel"`
	Kind    string `json:"kind"`
	Page    uint64 `json:"page"`
	GPU     int    `json:"gpu"`
	Request uint64 `json:"request"`
	StartPS uint64 `json:"start_ps"`
	EndPS   uint64 `json:"end_ps"`
	Bytes   uint64 `json:"bytes,omitempty"`
	Version uint64 `json:"version"`
}
type passiveLease struct {
	page    vm.Page
	managed bool
	virtual uint64
	gpu     int
	write   bool
	bits    map[uint32]bool
}
type passiveCache struct {
	name string
	gpu  int
	l2   bool
	dst  messaging.RemotePort
	port messaging.Port
}
type passiveFetch struct {
	job    *passiveTransition
	sector uint32
	dst    messaging.RemotePort
}
type passiveTransition struct {
	req          memprotocol.AccessReq
	gpu          int
	page         uint64
	bits         map[uint32]bool
	step         int
	queue        []memcontrolprotocol.Req
	pending      map[uint64]messaging.RemotePort
	fetchQueue   []memprotocol.ReadReq
	fetchPending int
	data         map[uint32][]byte
	start        timing.VTimeInPicoSec
	controlStart timing.VTimeInPicoSec
	fetchStart   timing.VTimeInPicoSec
	serviceStart timing.VTimeInPicoSec
	due          timing.VTimeInPicoSec
	need         map[uint32]bool
	cold         bool
	home         bool
	fallback     bool
	reused       bool
	frame        uint64
	version      uint64
	event        PassiveEvent
}

// Passive validates EVERY managed request, including TLB hits. Writes only
// record metadata and go home. Reader jobs hold one page admission lock, wait
// for that page's leases, invalidate only clean target lines, fetch through
// the actual home L2, and publish repaired sectors. No cache is globally paused.
// One job per GPU runs at once; jobs for different pages/GPUs can overlap.
type Passive struct {
	initialInput   hash.Hash
	s              *Session
	comp           *modeling.Component[struct{}, struct{}, modeling.None]
	ats            []*addresstranslator.Comp
	cps            []*cp.Comp
	caches         []passiveCache
	ports          []messaging.Port
	readPorts      map[messaging.RemotePort]messaging.Port
	homeModules    map[int]*rdma.Comp
	fetches        map[uint64]passiveFetch
	leases         map[uint64]passiveLease
	jobs           map[uint64]*passiveTransition
	queues         map[int][]*passiveTransition
	active         map[int]*passiveTransition
	locks          map[uint64]*passiveTransition
	grants         map[uint64]*passiveTransition
	writers        map[[2]uint64]map[uint32]bool
	writing        map[uint64]bool
	writesInflight map[uint64]uint64
	lastCopyKernel map[[2]uint64]int
	repaired       map[uint64]bool
	history        map[[2]uint64]map[uint32]bool
	currentReads   map[[2]uint64]map[uint32]bool
	readVersion    map[[2]uint64]uint64
	homeSeen       map[[2]uint64]map[uint32]uint64
	kernel         int
}

func validatePassivePolicy(policy string) {
	switch policy {
	case "inv", "retain", "i1-adaptive":
		return
	}
	panic("kernel-internal supports only inv, retain, i1-adaptive")
}
func EnablePassive(s *Session) {
	validatePassivePolicy(s.Config.Policy)
	if s.Plan != nil {
		panic("kernel-internal protocol cannot consume a future access plan")
	}
	p := &Passive{s: s, initialInput: sha256.New(), leases: map[uint64]passiveLease{}, jobs: map[uint64]*passiveTransition{},
		queues: map[int][]*passiveTransition{}, active: map[int]*passiveTransition{}, locks: map[uint64]*passiveTransition{}, grants: map[uint64]*passiveTransition{},
		writers: map[[2]uint64]map[uint32]bool{}, writing: map[uint64]bool{}, writesInflight: map[uint64]uint64{}, lastCopyKernel: map[[2]uint64]int{},
		repaired: map[uint64]bool{}, history: map[[2]uint64]map[uint32]bool{}, currentReads: map[[2]uint64]map[uint32]bool{}, readVersion: map[[2]uint64]uint64{}, homeSeen: map[[2]uint64]map[uint32]uint64{},
		readPorts: map[messaging.RemotePort]messaging.Port{}, homeModules: map[int]*rdma.Comp{}, fetches: map[uint64]passiveFetch{}}
	s.Passive = p
	s.Stats.Passive = &PassiveStats{Protocol: "kernel-internal-lazy-v2", AdaptiveRule: "previous observed read-interval footprint; per-sector version validation on every actual request; no future trace", RepairsByTrigger: map[string]uint64{}, RepairGranularities: map[uint64]Granularity{}}
	s.Stats.Adapter = "core-kernel-internal-lazy-v2"
	s.Stats.ModelRevision = "replica-kernel-internal-lazy-v2"
	s.Stats.OracleMappingPreinstall = true
	p.comp = modeling.NewBuilder[struct{}, struct{}, modeling.None]().WithEngine(s.sim.GetEngine()).WithFreq(timing.GHz).Build("ReplicaProtocol")
	p.comp.AddMiddleware(p)
	s.sim.RegisterComponent(p.comp)
	for _, component := range s.sim.Components() {
		if at, ok := component.(*addresstranslator.Comp); ok {
			p.ats = append(p.ats, at)
			addresstranslator.SetAccessController(at, p)
		}
		if c, ok := component.(*cp.Comp); ok {
			p.cps = append(p.cps, c)
		}
		if c, ok := component.(*rdma.Comp); ok {
			var gpu int
			if _, e := fmt.Sscanf(c.Name(), "GPU[%d]", &gpu); e == nil {
				p.homeModules[gpu] = c
			}
		}
		l2 := false
		switch c := component.(type) {
		case *writeback.Comp:
			l2 = true
			top := c.GetPortByName("Top")
			port := p.newPort(fmt.Sprintf("Fetch[%d]", len(p.readPorts)), false, top)
			p.readPorts[top.AsRemote()] = port
		case *writethroughcache.Comp:
			if strings.Contains(component.Name(), "L1I") {
				continue
			}
		default:
			continue
		}
		var gpu int
		if _, err := fmt.Sscanf(component.Name(), "GPU[%d]", &gpu); err != nil {
			panic("replica cache without GPU identity")
		}
		control := component.(messaging.Component).GetPortByName("Control")
		port := p.newPort(fmt.Sprintf("Control[%d]", len(p.ports)), true, control)
		p.ports = append(p.ports, port)
		p.caches = append(p.caches, passiveCache{component.Name(), gpu, l2, control.AsRemote(), port})
	}
	sort.Slice(p.caches, func(i, j int) bool { return p.caches[i].name < p.caches[j].name })
}
func (p *Passive) newPort(name string, control bool, peer messaging.Port) messaging.Port {
	if control {
		p.comp.DeclarePort(name, memcontrolprotocol.Requester)
	} else {
		p.comp.DeclarePort(name, memprotocol.Requester)
	}
	port := modeling.MakePortBuilder().WithRegistrar(p.s.sim).WithComponent(p.comp).WithSpec(modeling.PortSpec{BufSize: 64}).Build(name)
	p.comp.AssignPort(name, port)
	peer.(interface{ Connection() messaging.Connection }).Connection().PlugIn(port)
	return port
}
func (p *Passive) BeginKernel(kernel int) {
	if len(p.jobs) > 0 || len(p.leases) > 0 {
		panic("kernel boundary with pending protocol work")
	}
	p.kernel = kernel
	p.writers = map[[2]uint64]map[uint32]bool{}
	// Real boundary cache flush has completed; no old home L1 tags survive.
	p.homeSeen = map[[2]uint64]map[uint32]uint64{}
	for page, st := range p.s.pages {
		for gpu := range p.homeModules {
			key := [2]uint64{page, uint64(gpu)}
			p.homeSeen[key] = map[uint32]uint64{}
			for x, v := range st.LastWrite {
				p.homeSeen[key][x] = v
			}
		}
	}
}
func (p *Passive) sectors(req memprotocol.AccessReq) (map[uint32]bool, bool) {
	bits := map[uint32]bool{}
	w, write := req.(memprotocol.WriteReq)
	for off := uint64(0); off < req.GetByteSize(); off++ {
		if write && len(w.DirtyMask) > 0 && !w.DirtyMask[off] {
			continue
		}
		bits[uint32((req.GetAddress()%p.s.Config.PageBytes+off)/p.s.Config.SectorBytes)] = true
	}
	return bits, write
}
func (p *Passive) stale(st *pageState, c *copyState) map[uint32]bool {
	bits := map[uint32]bool{}
	for x := uint32(0); uint64(x) < p.s.Config.PageBytes/p.s.Config.SectorBytes; x++ {
		if !c.Valid[x] || c.SectorVersion[x] < st.LastWrite[x] {
			bits[x] = true
		}
	}
	return bits
}
func (p *Passive) valid(page uint64, c *copyState, bits map[uint32]bool) bool {
	if c == nil || c.Frame == 0 || !c.Had {
		return false
	}
	st := p.s.pages[page]
	if p.s.Config.Policy != "i1-adaptive" && c.Version != st.Version {
		return false
	}
	for x := range bits {
		if !c.Valid[x] || c.SectorVersion[x] < st.LastWrite[x] {
			return false
		}
	}
	return true
}
func (p *Passive) useHome(page uint64, gpu int, bits map[uint32]bool) bool {
	if gpu == int(p.s.pages[page].Home.DeviceID) {
		return true
	}
	// A GPU's own modified sectors always use home. Disjoint clean subblocks
	// remain eligible for S3; same-page false sharing is not a data race.
	for x := range bits {
		if p.writers[[2]uint64{page, uint64(gpu)}][x] {
			return true
		}
	}
	return false
}
func (p *Passive) homeNeedsValidation(page uint64, gpu int, bits map[uint32]bool) bool {
	for x := range bits {
		if p.homeSeen[[2]uint64{page, uint64(gpu)}][x] < p.s.pages[page].LastWrite[x] {
			return true
		}
	}
	return false
}
func (p *Passive) mustTransition(page uint64, gpu int, bits map[uint32]bool, write bool) bool {
	if write {
		return false
	}
	if p.useHome(page, gpu, bits) {
		return p.homeNeedsValidation(page, gpu, bits)
	}
	return !p.valid(page, p.s.pages[page].Copies[gpu], bits)
}
func (p *Passive) wake() {
	for _, at := range p.ats {
		at.TickLater()
	}
	if p.comp != nil {
		p.comp.TickLater()
	}
}
func (p *Passive) enqueue(req memprotocol.AccessReq, gpu int, page uint64, bits map[uint32]bool) {
	if p.jobs[req.Meta().ID] != nil {
		return
	}
	t := &passiveTransition{req: req, gpu: gpu, page: page, bits: bits, step: -1, pending: map[uint64]messaging.RemotePort{}, data: map[uint32][]byte{}, start: p.comp.CurrentTime()}
	p.jobs[req.Meta().ID] = t
	p.queues[gpu] = append(p.queues[gpu], t)
	p.s.Stats.Passive.PendingHighWater = max(p.s.Stats.Passive.PendingHighWater, uint64(len(p.jobs)))
	p.comp.TickLater()
}
func (p *Passive) Admit(at *addresstranslator.Comp, req memprotocol.AccessReq) bool {
	id := req.Meta().ID
	page := req.GetAddress() / p.s.Config.PageBytes * p.s.Config.PageBytes
	st := p.s.pages[page]
	managed := st != nil && !st.Retired && req.GetPID() == p.s.ctx.PID()
	if !managed {
		p.leases[id] = passiveLease{}
		return true
	}
	if req.GetAddress()+req.GetByteSize() > page+p.s.Config.PageBytes {
		panic("passive request crosses page boundary")
	}
	gpu := int(at.Spec().DeviceID)
	bits, write := p.sectors(req)
	grant := p.grants[id]
	if lock := p.locks[page]; lock != nil && lock != grant {
		return false
	}
	if p.jobs[id] != nil && grant == nil {
		return false
	}
	if !write && grant == nil && p.mustTransition(page, gpu, bits, false) {
		p.enqueue(req, gpu, page, bits)
		return false
	}
	frame := st.Home
	if write {
		p.markWrite(page, gpu, bits)
		p.writesInflight[page]++
	} else {
		p.s.Stats.Passive.VersionChecks++
		home := p.useHome(page, gpu, bits) || (grant != nil && grant.fallback)
		if !home {
			c := st.Copies[gpu]
			if !p.valid(page, c, bits) {
				p.s.Stats.Passive.StaleReadSectors += uint64(len(bits))
				panic("stale replica read blocked by version assertion")
			}
			frame.PAddr = c.Frame
			frame.DeviceID = uint64(gpu)
			p.s.Stats.ReplicaReadTransactions++
			p.s.clock++
			c.Stamp = p.s.clock
			if p.writing[page] && c.Version != st.Version {
				p.s.Stats.Passive.CleanReplicaReadsDuringWrite++
				p.s.Stats.WriteEpochExistingReads++
			}
			p.observeRead(page, gpu, bits)
		} else if grant != nil && grant.fallback {
			p.s.Stats.Passive.FallbackReads++
		}
	}
	if grant != nil {
		delete(p.grants, id)
		delete(p.jobs, id)
		delete(p.locks, page)
		p.wake()
	}
	p.leases[id] = passiveLease{page: frame, managed: true, virtual: page, gpu: gpu, write: write, bits: bits}
	stats := p.s.Stats.Passive
	stats.ManagedRequests++
	if write {
		stats.WriteRequests++
		if gpu == int(st.Home.DeviceID) {
			p.s.Stats.OwnerWriteTransactions++
		} else {
			p.s.Stats.NonOwnerWriteTransactions++
		}
	} else {
		stats.ReadRequests++
		if gpu == int(st.Home.DeviceID) {
			p.s.Stats.OwnerReadTransactions++
		} else {
			p.s.Stats.NonOwnerReadTransactions++
		}
	}
	stats.InflightHighWater = max(stats.InflightHighWater, uint64(len(p.leases)))
	return true
}
func (p *Passive) Resolve(_ *addresstranslator.Comp, id uint64, page vm.Page) vm.Page {
	l, ok := p.leases[id]
	if !ok {
		panic("passive translation without lease")
	}
	if l.managed {
		return l.page
	}
	return page
}
func (p *Passive) Complete(_ *addresstranslator.Comp, id uint64) {
	l, ok := p.leases[id]
	if !ok {
		panic("passive completed unknown request")
	}
	if l.managed && l.write {
		p.writesInflight[l.virtual]--
	}
	delete(p.leases, id)
	if len(p.jobs) > 0 {
		p.wake()
	}
}

// markWrite never traverses copies, releases a frame, or issues cache control.
func (p *Passive) markWrite(page uint64, gpu int, bits map[uint32]bool) {
	st := p.s.pages[page]
	st.Version++
	p.s.Stats.WritePageEpochs++
	key := [2]uint64{page, uint64(gpu)}
	if p.writers[key] == nil {
		p.writers[key] = map[uint32]bool{}
	}
	for x := range bits {
		st.LastWrite[x] = st.Version
		p.writers[key][x] = true
	}
	if p.repaired[page] {
		p.s.Stats.Passive.WriteAfterRepair++
		p.repaired[page] = false
	}
	p.writing[page] = true
}
func (p *Passive) observeRead(page uint64, gpu int, bits map[uint32]bool) {
	key := [2]uint64{page, uint64(gpu)}
	if p.readVersion[key] != p.s.pages[page].Version {
		if len(p.currentReads[key]) > 0 {
			p.history[key] = p.currentReads[key]
		}
		p.currentReads[key] = map[uint32]bool{}
		p.readVersion[key] = p.s.pages[page].Version
	}
	if p.currentReads[key] == nil {
		p.currentReads[key] = map[uint32]bool{}
	}
	for x := range bits {
		p.currentReads[key][x] = true
	}
}
func (p *Passive) pageBusy(page uint64) bool {
	for _, l := range p.leases {
		if l.managed && l.virtual == page {
			return true
		}
	}
	return false
}
func (p *Passive) frameBusy(frame uint64) bool {
	for _, t := range p.grants {
		if t.frame == frame {
			return true
		}
	}
	for _, l := range p.leases {
		if l.managed && l.page.PAddr == frame {
			return true
		}
	}
	return false
}
func (p *Passive) cacheReady() bool {
	for _, c := range p.cps {
		if c.State.CtrlSeq != "" && c.State.CtrlSeq != "none" {
			return false
		}
	}
	return true
}
func (p *Passive) sortedGPUs() []int {
	xs := []int{}
	for g := range p.queues {
		xs = append(xs, g)
	}
	sort.Ints(xs)
	return xs
}
func (p *Passive) Tick() bool {
	progress := p.responses()
	for _, gpu := range p.sortedGPUs() {
		t := p.active[gpu]
		if t == nil && len(p.queues[gpu]) > 0 {
			candidate := p.queues[gpu][0]
			if p.locks[candidate.page] == nil && p.cacheReady() {
				t = candidate
				p.active[gpu] = t
				p.locks[t.page] = t
				p.queues[gpu] = p.queues[gpu][1:]
				progress = true
				p.s.Stats.Passive.ConcurrentRepairsHighWater = max(p.s.Stats.Passive.ConcurrentRepairsHighWater, uint64(len(p.active)))
			}
		}
		if t == nil {
			continue
		}
		progress = p.advance(t) || progress
	}
	return progress
}
func (p *Passive) responses() bool {
	progress := false
	for _, port := range p.ports {
		for port.PeekIncoming() != nil {
			rsp, ok := port.RetrieveIncoming().(memcontrolprotocol.Rsp)
			if !ok || !rsp.Success {
				panic(fmt.Sprintf("replica range control failed: %+v", rsp))
			}
			found := false
			for _, t := range p.active {
				if dst, ok := t.pending[rsp.RspTo]; ok {
					if dst != rsp.Src {
						panic("range acknowledgement source mismatch")
					}
					delete(t.pending, rsp.RspTo)
					found = true
					break
				}
			}
			if !found {
				panic("unknown range acknowledgement")
			}
			progress = true
		}
	}
	// Sort ports to preserve determinism when concurrent source responses arrive.
	dsts := []string{}
	for dst := range p.readPorts {
		dsts = append(dsts, string(dst))
	}
	sort.Strings(dsts)
	for _, dst := range dsts {
		port := p.readPorts[messaging.RemotePort(dst)]
		for port.PeekIncoming() != nil {
			rsp, ok := port.RetrieveIncoming().(memprotocol.DataReadyRsp)
			if !ok {
				panic("unexpected home fetch response")
			}
			f, ok := p.fetches[rsp.RspTo]
			if !ok || f.dst != rsp.Src || uint64(len(rsp.Data)) != p.s.Config.SectorBytes {
				panic("home fetch response mismatch")
			}
			f.job.data[f.sector] = append([]byte(nil), rsp.Data...)
			f.job.fetchPending--
			delete(p.fetches, rsp.RspTo)
			progress = true
		}
	}
	return progress
}
func (p *Passive) advance(t *passiveTransition) bool {
	switch t.step {
	case -1:
		if p.pageBusy(t.page) {
			return false
		}
		if p.writesInflight[t.page] != 0 {
			panic("copy started with home writes in flight")
		}
		p.prepare(t)
		t.controlStart = p.comp.CurrentTime()
		p.issueInvalidation(t)
		t.step = 0
		return true
	case 0:
		if len(t.queue) > 0 {
			left := t.queue[:0]
			for _, req := range t.queue {
				port := p.controlPort(req.Src)
				if port.CanSend() {
					port.Send(req)
				} else {
					left = append(left, req)
				}
			}
			t.queue = left
			return true
		}
		if len(t.pending) > 0 {
			return false
		}
		p.s.Stats.Passive.CacheControlPS += uint64(p.comp.CurrentTime() - t.controlStart)
		if t.home {
			p.finishHome(t)
			return true
		}
		p.beginFetch(t)
		t.step = 1
		return true
	case 1:
		if len(t.fetchQueue) > 0 {
			left := t.fetchQueue[:0]
			for _, req := range t.fetchQueue {
				port := p.readPorts[req.Dst]
				if port.CanSend() {
					port.Send(req)
				} else {
					left = append(left, req)
				}
			}
			t.fetchQueue = left
			return true
		}
		if t.fetchPending > 0 {
			return false
		}
		p.s.Stats.Passive.HomeFetchPS += uint64(p.comp.CurrentTime() - t.fetchStart)
		p.reserveTransfer(t)
		t.step = 2
		return true
	case 2:
		if p.comp.CurrentTime() < t.due {
			return true
		}
		p.finishTransfer(t)
		p.grant(t)
		return true
	}
	panic("invalid reader job state")
}
func (p *Passive) prepare(t *passiveTransition) {
	st := p.s.pages[t.page]
	t.version = st.Version
	t.home = p.useHome(t.page, t.gpu, t.bits)
	t.event = PassiveEvent{Kernel: p.kernel, Page: t.page, GPU: t.gpu, Request: t.req.Meta().ID, StartPS: uint64(t.start), Version: st.Version, Kind: "home-validate"}
	if t.home {
		t.frame = st.Home.PAddr
		t.need = t.bits
		return
	}
	c := st.Copies[t.gpu]
	if c == nil {
		c = &copyState{Valid: map[uint32]bool{}, SectorVersion: map[uint32]uint64{}}
		st.Copies[t.gpu] = c
	}
	// History is observed at the reader, never walked/updated on another GPU's write.
	p.observeRead(t.page, t.gpu, map[uint32]bool{})
	if c.Frame == 0 && !p.allocate(t, c) {
		t.home = true
		t.fallback = true
		t.frame = st.Home.PAddr
		t.need = t.bits
		t.event.Kind = "home-fallback"
		return
	}
	t.frame = c.Frame
	t.cold = !c.Had
	t.need = map[uint32]bool{}
	if !c.Had || p.s.Config.Policy == "inv" {
		for x := uint32(0); uint64(x) < p.s.Config.PageBytes/p.s.Config.SectorBytes; x++ {
			t.need[x] = true
		}
	} else {
		stale := p.stale(st, c)
		for x := range t.bits {
			if stale[x] {
				t.need[x] = true
			}
		}
		if p.s.Config.Policy == "retain" {
			t.need = stale
		} else {
			demand := p.s.expandRepair(t.need)
			need, full := p.onlineRepair(t.page, t.gpu, c, demand)
			if full {
				p.s.Stats.Passive.FullStaleRepairs++
				p.s.Stats.Passive.ExtraRepairBytes += uint64(len(need)-len(demand)) * p.s.Config.SectorBytes
			} else {
				p.s.Stats.Passive.DemandRepairs++
			}
			t.need = need
		}
	}
	t.need = p.s.expandRepair(t.need)
	t.event.Kind = "repair"
	if t.cold {
		t.event.Kind = "cold-copy"
	}
	t.event.Bytes = uint64(len(t.need)) * p.s.Config.SectorBytes
	stats := p.s.Stats.Passive
	if t.cold {
		stats.RepairsByTrigger["cold"]++
	} else {
		stats.RepairsByTrigger["stale-version"]++
		p.s.Stats.Invalidations++
	}
	if t.reused {
		stats.RepairsByTrigger["frame-reuse"]++
	}
}
func (p *Passive) allocate(t *passiveTransition, c *copyState) bool {
	limit := p.s.Config.ReplicaPagesPerGPU
	count := 0
	var victim *copyState
	for _, st := range p.s.pages {
		if v := st.Copies[t.gpu]; v != nil && v.Frame != 0 {
			count++
			if !p.frameBusy(v.Frame) && (victim == nil || v.Stamp < victim.Stamp || (v.Stamp == victim.Stamp && v.Frame < victim.Frame)) {
				victim = v
			}
		}
	}
	if limit > 0 && count >= limit {
		if victim == nil {
			return false
		}
		c.Frame = victim.Frame
		victim.Frame = 0
		victim.Valid = map[uint32]bool{}
		victim.Had = false
		t.reused = true
		p.s.Stats.Evictions++
		return true
	}
	frame, ok := p.s.d.TryAllocateReplicaFrame(t.gpu)
	if !ok {
		return false
	}
	c.Frame = frame
	return true
}
func (p *Passive) lineAddresses(frame uint64, bits map[uint32]bool) []uint64 {
	seen := map[uint64]bool{}
	for x := range bits {
		lo := uint64(x) * p.s.Config.SectorBytes
		hi := lo + p.s.Config.SectorBytes
		for off := lo / 64 * 64; off < hi; off += 64 {
			seen[frame+off] = true
		}
	}
	xs := []uint64{}
	for a := range seen {
		xs = append(xs, a)
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	return xs
}
func (p *Passive) issueInvalidation(t *passiveTransition) {
	bits := t.need
	// Newly allocated frames can have tags left by retired allocations. All tags
	// are cleaned before publication, even when this is the first logical copy.
	if t.cold && !t.home {
		bits = map[uint32]bool{}
		for x := uint32(0); uint64(x) < p.s.Config.PageBytes/p.s.Config.SectorBytes; x++ {
			bits[x] = true
		}
	}
	addresses := p.lineAddresses(t.frame, bits)
	if len(addresses) == 0 {
		return
	}
	if t.home {
		p.s.Stats.Passive.HomeL1InvalidateEvents++
	} else {
		p.s.Stats.Passive.ReplicaInvalidateEvents++
		p.s.Stats.Passive.ReplicaInvalidateBytes += uint64(len(addresses)) * 64
	}
	for _, c := range p.caches {
		if c.gpu != t.gpu || (t.home && (c.l2 || strings.Contains(c.name, "MALL"))) {
			continue
		}
		req := memcontrolprotocol.Req{MsgMeta: messaging.MsgMeta{ID: timing.GetIDGenerator().Generate(), Src: c.port.AsRemote(), Dst: c.dst, TrafficClass: "memcontrolprotocol.Req"}, Command: memcontrolprotocol.CmdInvalidateCleanRange, Addresses: addresses, PID: 0}
		t.pending[req.ID] = req.Dst
		t.queue = append(t.queue, req)
	}
}
func (p *Passive) controlPort(src messaging.RemotePort) messaging.Port {
	for _, port := range p.ports {
		if port.AsRemote() == src {
			return port
		}
	}
	panic("unknown control source")
}
func (p *Passive) finishHome(t *passiveTransition) {
	key := [2]uint64{t.page, uint64(t.gpu)}
	if p.homeSeen[key] == nil {
		p.homeSeen[key] = map[uint32]uint64{}
	}
	for x := range t.bits {
		p.homeSeen[key][x] = p.s.pages[t.page].LastWrite[x]
	}
	p.grant(t)
}

// AT physical requests use PID zero. Both fetch and invalidation must use
// the same physical cache namespace, never the virtual address-space PID.
func (p *Passive) beginFetch(t *passiveTransition) {
	t.fetchStart = p.comp.CurrentTime()
	st := p.s.pages[t.page]
	for _, x := range sorted(t.need) {
		addr := st.Home.PAddr + uint64(x)*p.s.Config.SectorBytes
		module := p.homeModules[int(st.Home.DeviceID)]
		if module == nil {
			panic("missing authoritative L2 mapper")
		}
		dst := module.Resources().LocalModules.Find(addr)
		port := p.readPorts[dst]
		if port == nil {
			panic("home mapper is not an attached L2")
		}
		req := memprotocol.ReadReq{MsgMeta: messaging.MsgMeta{ID: timing.GetIDGenerator().Generate(), Src: port.AsRemote(), Dst: dst, TrafficClass: "memprotocol.ReadReq", TrafficBytes: 16}, Address: addr, AccessByteSize: p.s.Config.SectorBytes, PID: 0}
		t.fetchQueue = append(t.fetchQueue, req)
		p.fetches[req.ID] = passiveFetch{t, x, dst}
		t.fetchPending++
		p.s.Stats.Passive.HomeFetchRequests++
		p.s.Stats.Passive.HomeFetchBytes += p.s.Config.SectorBytes
	}
}
func (p *Passive) reserveTransfer(t *passiveTransition) {
	now := p.comp.CurrentTime()
	t.serviceStart = now
	ready := now + timing.VTimeInPicoSec(p.s.Config.FaultNS)*1000
	t.due = ready
	xs := sorted(t.need)
	for lo := 0; lo < len(xs); {
		hi := lo + 1
		for hi < len(xs) && xs[hi] == xs[hi-1]+1 {
			hi++
		}
		t.due = max(t.due, p.s.Fabric.Reserve(gpuName(int(p.s.pages[t.page].Home.DeviceID)), gpuName(t.gpu), uint64(hi-lo)*p.s.Config.SectorBytes+16, ready))
		lo = hi
	}
	p.s.Stats.Passive.CopyServicePS += uint64(t.due - now)
}
func (p *Passive) finishTransfer(t *passiveTransition) {
	s := p.s
	st := s.pages[t.page]
	c := st.Copies[t.gpu]
	if st.Version != t.version {
		panic("page changed while reader repair held admission lock")
	}
	for x := range t.need {
		data := t.data[x]
		if uint64(len(data)) != s.Config.SectorBytes {
			panic("missing actual home response")
		}
		must(s.d.Resources().GlobalStorage.Write(c.Frame+uint64(x)*s.Config.SectorBytes, data))
		c.Valid[x] = true
		c.SectorVersion[x] = st.LastWrite[x]
	}
	if len(p.stale(st, c)) == 0 {
		c.Version = st.Version
	}
	key := [2]uint64{t.page, uint64(t.gpu)}
	p.repaired[t.page] = !t.cold
	s.Stats.Faults++
	s.Stats.CopyBytes += t.event.Bytes
	if t.cold {
		s.Stats.ColdCopies++
	} else {
		if s.Config.Policy == "inv" {
			s.Stats.Refills++
			if p.lastCopyKernel[key] == p.kernel {
				s.Stats.Passive.IntrakernelRefills++
			}
		} else {
			s.Stats.Repairs++
			if p.lastCopyKernel[key] == p.kernel {
				s.Stats.Passive.IntrakernelRepairs++
			}
		}
		s.countGranularities(t.need)
		stats := s.Stats.Passive
		stats.RepairEvents++
		stats.RepairBytes += t.event.Bytes
		for _, g := range []uint64{32, 128, 512, 4096} {
			if g < s.Config.SectorBytes || g > s.Config.PageBytes {
				continue
			}
			v := stats.RepairGranularities[g]
			layout := intervals(t.need, s.Config.SectorBytes, g)
			v.Bytes += layout.Bytes
			v.Blocks += layout.Blocks
			v.Extents += layout.Extents
			stats.RepairGranularities[g] = v
		}
	}
	p.lastCopyKernel[key] = p.kernel
	event := RecoveryEvent{HomeGPU: int(st.Home.DeviceID), Role: "runtime", RepairKind: s.Config.Policy, Epoch: p.kernel, Page: t.page, GPU: t.gpu, Version: st.Version, Cold: t.cold, Bytes: t.event.Bytes, ChargedBytes: t.event.Bytes, StartPS: uint64(t.serviceStart), DuePS: uint64(t.due), Layouts: map[uint64]Granularity{}}
	for _, g := range []uint64{32, 128, 512, 4096} {
		if g >= s.Config.SectorBytes && g <= s.Config.PageBytes {
			event.Layouts[g] = intervals(t.need, s.Config.SectorBytes, g)
		}
	}
	s.Stats.RecoveryEvents = append(s.Stats.RecoveryEvents, event)
	c.Had = true
	c.Readable = true
	s.clock++
	c.Stamp = s.clock
	s.snapshot()
}
func (p *Passive) grant(t *passiveTransition) {
	t.event.EndPS = uint64(p.comp.CurrentTime())
	p.s.Stats.Passive.Events = append(p.s.Stats.Passive.Events, t.event)
	p.s.Stats.Passive.Transitions++
	p.grants[t.req.Meta().ID] = t
	delete(p.active, t.gpu)
	p.wake()
}

// Prediction never supplies validity. Every repaired sector arrives from a real
// home L2 response; partial repair never advances another sector's version.
func (p *Passive) onlineRepair(page uint64, gpu int, c *copyState, demand map[uint32]bool) (map[uint32]bool, bool) {
	stale := p.stale(p.s.pages[page], c)
	full := p.s.expandRepair(stale)
	predicted := map[uint32]bool{}
	for x := range p.history[[2]uint64{page, uint64(gpu)}] {
		if stale[x] && !demand[x] {
			predicted[x] = true
		}
	}
	if len(predicted) > 0 && p.s.repairServicePS(full) < p.s.repairServicePS(demand)+p.s.repairServicePS(p.s.expandRepair(predicted)) {
		return full, true
	}
	return demand, false
}
func (p *Passive) HostWrite(page uint64, bits map[uint32]bool) { p.markWrite(page, 0, bits) }
func (p *Passive) InitialWrite(ptr uint64, src interface{}) {
	must(binary.Write(p.initialInput, binary.LittleEndian, ptr))
	must(binary.Write(p.initialInput, binary.LittleEndian, int64(binary.Size(src))))
	must(binary.Write(p.initialInput, binary.LittleEndian, src))
}
func (p *Passive) Finish() {
	p.s.Stats.InitialInputSHA256 = fmt.Sprintf("%x", p.initialInput.Sum(nil))
	if len(p.jobs) > 0 || len(p.leases) > 0 || len(p.grants) > 0 || len(p.fetches) > 0 || len(p.locks) > 0 {
		panic("kernel-internal protocol did not drain")
	}
	for _, at := range p.ats {
		addresstranslator.SetAccessController(at, nil)
	}
}

// CheckAtBarrier verifies actual bytes after real home writeback. It is a
// functional invariant, not a simulated coherence operation or timing charge.
func (p *Passive) CheckAtBarrier() {
	if len(p.jobs) > 0 || len(p.leases) > 0 {
		panic("boundary with pending reader work")
	}
	for page, st := range p.s.pages {
		if st.Retired {
			continue
		}
		for gpu, c := range st.Copies {
			if c.Frame == 0 {
				continue
			}
			for x := range c.Valid {
				if c.SectorVersion[x] < st.LastWrite[x] {
					continue
				}
				off := uint64(x) * p.s.Config.SectorBytes
				want, e := p.s.d.Resources().GlobalStorage.Read(st.Home.PAddr+off, p.s.Config.SectorBytes)
				must(e)
				got, e := p.s.d.Resources().GlobalStorage.Read(c.Frame+off, p.s.Config.SectorBytes)
				must(e)
				if !bytes.Equal(want, got) {
					panic(fmt.Sprintf("replica bytes mismatch kernel=%d page=%x gpu=%d sector=%d replicaVersion=%d lastWrite=%d want=%x got=%x", p.kernel, page, gpu, x, c.SectorVersion[x], st.LastWrite[x], want, got))
				}
			}
		}
	}
}
