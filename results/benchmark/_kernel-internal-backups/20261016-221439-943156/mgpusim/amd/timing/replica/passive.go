package replica

import (
	"fmt"
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
)

// PassiveStats are measured inside running kernels. Service and cache-control
// times are INCLUDED in kernel_ps and must never be added to application_ps.
type PassiveStats struct {
	Protocol                     string         `json:"protocol"`
	AdaptiveRule                 string         `json:"adaptive_rule"`
	ManagedRequests              uint64         `json:"managed_requests"`
	ReadRequests                 uint64         `json:"read_requests"`
	WriteRequests                uint64         `json:"write_requests"`
	Transitions                  uint64         `json:"transitions"`
	CacheControlPS               uint64         `json:"cache_control_ps_in_kernel"`
	CopyServicePS                uint64         `json:"copy_service_ps_in_kernel"`
	InflightHighWater            uint64         `json:"inflight_high_water"`
	PendingHighWater             uint64         `json:"pending_high_water"`
	IntrakernelRefills           uint64         `json:"intrakernel_refills"`
	IntrakernelRepairs           uint64         `json:"intrakernel_repairs"`
	WriteAfterRepair             uint64         `json:"write_after_repair_invalidations"`
	CleanReplicaReadsDuringWrite uint64         `json:"clean_replica_reads_during_write"`
	DemandRepairs                uint64         `json:"adaptive_demand_repairs"`
	FullStaleRepairs             uint64         `json:"adaptive_full_stale_repairs"`
	ExtraRepairBytes             uint64         `json:"adaptive_extra_repair_bytes"`
	Events                       []PassiveEvent `json:"events"`
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
}
type passiveCache struct {
	name string
	l2   bool
	dst  messaging.RemotePort
	port messaging.Port
}
type passiveTransition struct {
	req          memprotocol.AccessReq
	gpu          int
	page         uint64
	bits         map[uint32]bool
	write        bool
	step         int
	queue        []memcontrolprotocol.Req
	pending      map[uint64]messaging.RemotePort
	start        timing.VTimeInPicoSec
	serviceStart timing.VTimeInPicoSec
	due          timing.VTimeInPicoSec
	need         map[uint32]bool
	cold         bool
	event        PassiveEvent
}

// Passive checks permission on EVERY actual request before any data-cache hit.
// One directory transition at a time stops new AT admission, drains accepted
// requests, and uses real filtered cache Drain/Flush/Invalidate/Enable messages.
// This conservative global quiescence is charged equally to all policies. It
// is an executable coherence model, not a calibrated distributed controller.
type Passive struct {
	s              *Session
	comp           *modeling.Component[struct{}, struct{}, modeling.None]
	ats            []*addresstranslator.Comp
	cps            []*cp.Comp
	caches         []passiveCache
	ports          []messaging.Port
	leases         map[uint64]passiveLease
	next           *passiveTransition
	granted        uint64
	writer         map[uint64]int
	writing        map[uint64]bool
	lastCopyKernel map[[2]uint64]int
	repaired       map[[2]uint64]bool
	history        map[[2]uint64]map[uint32]bool
	currentReads   map[[2]uint64]map[uint32]bool
	kernel         int
}

func EnablePassive(s *Session) {
	if s.Plan != nil {
		panic("kernel-internal protocol cannot consume a future access plan")
	}
	p := &Passive{s: s, leases: map[uint64]passiveLease{}, writer: map[uint64]int{}, writing: map[uint64]bool{},
		lastCopyKernel: map[[2]uint64]int{}, repaired: map[[2]uint64]bool{},
		history: map[[2]uint64]map[uint32]bool{}, currentReads: map[[2]uint64]map[uint32]bool{}}
	s.Passive = p
	s.Stats.Passive = &PassiveStats{Protocol: "kernel-internal-online-v1", AdaptiveRule: "previous read-interval footprint; no future trace or kernel write set"}
	s.Stats.Adapter = "core-kernel-internal-online-v1"
	s.Stats.ModelRevision = "replica-kernel-internal-v1"
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
		l2 := false
		switch component.(type) {
		case *writeback.Comp:
			l2 = true
		case *writethroughcache.Comp:
			if strings.Contains(component.Name(), "L1I") {
				continue
			}
		default:
			continue
		}
		control := component.(messaging.Component).GetPortByName("Control")
		conn := control.(interface{ Connection() messaging.Connection }).Connection()
		name := fmt.Sprintf("Control[%d]", len(p.ports))
		p.comp.DeclarePort(name, memcontrolprotocol.Requester)
		port := modeling.MakePortBuilder().WithRegistrar(s.sim).WithComponent(p.comp).WithSpec(modeling.PortSpec{BufSize: 8}).Build(name)
		p.comp.AssignPort(name, port)
		conn.PlugIn(port)
		p.ports = append(p.ports, port)
		p.caches = append(p.caches, passiveCache{component.Name(), l2, control.AsRemote(), port})
	}
	sort.Slice(p.caches, func(i, j int) bool { return p.caches[i].name < p.caches[j].name })
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

func (p *Passive) Admit(at *addresstranslator.Comp, req memprotocol.AccessReq) bool {
	id := req.Meta().ID
	if p.next != nil {
		return false
	}
	if p.granted != 0 && p.granted != id {
		return false
	}
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
	if p.granted != id && p.mustTransition(page, gpu, bits, write) {
		p.next = &passiveTransition{req: req, gpu: gpu, page: page, bits: bits, write: write, step: -1,
			pending: map[uint64]messaging.RemotePort{}, start: p.comp.CurrentTime()}
		p.s.Stats.Passive.PendingHighWater = max(p.s.Stats.Passive.PendingHighWater, 1)
		p.comp.TickLater()
		return false
	}
	p.granted = 0
	frame := st.Home
	key := [2]uint64{page, uint64(gpu)}
	c := st.Copies[gpu]
	if write {
		p.markWrite(page, gpu, bits)
	} else if gpu != int(st.Home.DeviceID) && p.valid(c, bits) {
		frame.PAddr = c.Frame
		frame.DeviceID = uint64(gpu)
		p.s.Stats.ReplicaReadTransactions++
		if p.writing[page] {
			p.s.Stats.Passive.CleanReplicaReadsDuringWrite++
			p.s.Stats.WriteEpochExistingReads++
		}
		if p.currentReads[key] == nil {
			p.currentReads[key] = map[uint32]bool{}
		}
		for x := range bits {
			p.currentReads[key][x] = true
		}
	} else if gpu != int(st.Home.DeviceID) {
		panic("remote read admitted without a valid replica")
	}
	p.leases[id] = passiveLease{frame, true}
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

func (p *Passive) valid(c *copyState, bits map[uint32]bool) bool {
	if c == nil || c.Frame == 0 || (p.s.Config.Policy == "retain" && !c.Readable) {
		return false
	}
	for x := range bits {
		if !c.Valid[x] {
			return false
		}
	}
	return true
}

func (p *Passive) mustTransition(page uint64, gpu int, bits map[uint32]bool, write bool) bool {
	st := p.s.pages[page]
	if write {
		if !p.writing[page] || p.writer[page] != gpu {
			return true
		}
		for _, c := range st.Copies {
			if c.Frame == 0 {
				continue
			}
			if p.s.Config.Policy == "inv" || (p.s.Config.Policy == "retain" && c.Readable) {
				return true
			}
			for x := range bits {
				if c.Valid[x] {
					return true
				}
			}
		}
		return false
	}
	if gpu == int(st.Home.DeviceID) {
		return p.writing[page]
	}
	return !p.valid(st.Copies[gpu], bits)
}

func (p *Passive) Resolve(_ *addresstranslator.Comp, id uint64, page vm.Page) vm.Page {
	l, ok := p.leases[id]
	if !ok {
		panic("passive translation without permission lease")
	}
	if l.managed {
		return l.page
	}
	return page
}
func (p *Passive) Complete(_ *addresstranslator.Comp, id uint64) {
	if _, ok := p.leases[id]; !ok {
		panic("passive completed unknown request")
	}
	delete(p.leases, id)
	if p.next != nil {
		p.comp.TickLater()
	}
}

func (p *Passive) markWrite(page uint64, gpu int, bits map[uint32]bool) {
	st := p.s.pages[page]
	st.Version++
	p.s.Stats.WritePageEpochs++
	for x := range bits {
		st.LastWrite[x] = st.Version
	}
	for g, c := range st.Copies {
		key := [2]uint64{page, uint64(g)}
		if len(p.currentReads[key]) > 0 {
			p.history[key] = p.currentReads[key]
			p.currentReads[key] = map[uint32]bool{}
		}
		if c.Frame == 0 {
			continue
		}
		changed := false
		for x := range bits {
			if c.Valid[x] {
				changed = true
			}
			delete(c.Valid, x)
		}
		if p.s.Config.Policy == "inv" {
			p.s.Stats.Invalidations++
			p.s.release(g, c)
		} else if p.s.Config.Policy == "retain" {
			if c.Readable {
				p.s.Stats.Invalidations++
				changed = true
			}
			c.Readable = false
		} else if changed {
			p.s.Stats.Invalidations++
		}
		if changed && p.repaired[key] {
			p.s.Stats.Passive.WriteAfterRepair++
			p.repaired[key] = false
		}
	}
	p.writing[page] = true
	p.writer[page] = gpu
}

// Tick advances only the active permission transition. No host barrier or
// recursive engine.Run is used while kernels are executing.
func (p *Passive) Tick() bool {
	t := p.next
	if t == nil {
		return false
	}
	for _, port := range p.ports {
		for port.PeekIncoming() != nil {
			rsp, ok := port.RetrieveIncoming().(memcontrolprotocol.Rsp)
			if !ok || !rsp.Success {
				panic(fmt.Sprintf("passive cache control failed: %+v", rsp))
			}
			if dst, ok := t.pending[rsp.RspTo]; !ok || dst != rsp.Src {
				panic("passive unexpected control acknowledgement")
			}
			delete(t.pending, rsp.RspTo)
		}
	}
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
	if t.step == -1 {
		if len(p.leases) > 0 {
			return false
		}
		// Kernel-start cache control must finish before the directory owns caches.
		for _, c := range p.cps {
			if c.State.CtrlSeq != "" && c.State.CtrlSeq != "none" {
				return true
			}
		}
	}
	if t.step == 6 {
		p.s.Stats.Passive.CacheControlPS += uint64(p.comp.CurrentTime() - t.start)
		p.prepareTransfer(t)
		t.step++
		return true
	}
	if t.step == 7 {
		if p.comp.CurrentTime() < t.due {
			return true
		}
		p.finishTransfer(t)
		t.event.EndPS = uint64(p.comp.CurrentTime())
		p.s.Stats.Passive.Events = append(p.s.Stats.Passive.Events, t.event)
		p.s.Stats.Passive.Transitions++
		p.granted = t.req.Meta().ID
		p.next = nil
		for _, at := range p.ats {
			at.TickLater()
		}
		return false
	}
	t.step++
	p.issueStep(t)
	return true
}

func (p *Passive) controlPort(src messaging.RemotePort) messaging.Port {
	for _, port := range p.ports {
		if port.AsRemote() == src {
			return port
		}
	}
	panic("unknown control source")
}

func (p *Passive) addresses(t *passiveTransition) []uint64 {
	st := p.s.pages[t.page]
	frames := []uint64{st.Home.PAddr}
	for _, c := range st.Copies {
		if c.Frame != 0 {
			frames = append(frames, c.Frame)
		}
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i] < frames[j] })
	result := []uint64{}
	for _, frame := range frames {
		for off := uint64(0); off < p.s.Config.PageBytes; off += 64 {
			result = append(result, frame+off)
		}
	}
	return result
}

func (p *Passive) issueStep(t *passiveTransition) {
	for _, c := range p.caches {
		var cmd memcontrolprotocol.Command
		switch t.step {
		case 0:
			if c.l2 {
				continue
			}
			cmd = memcontrolprotocol.CmdDrain
		case 1:
			if !c.l2 {
				continue
			}
			cmd = memcontrolprotocol.CmdDrain
		case 2:
			if !c.l2 {
				continue
			}
			cmd = memcontrolprotocol.CmdFlush
		case 3:
			if c.l2 {
				continue
			}
			cmd = memcontrolprotocol.CmdInvalidate
		case 4:
			if !c.l2 {
				continue
			}
			cmd = memcontrolprotocol.CmdInvalidate
		case 5:
			cmd = memcontrolprotocol.CmdEnable
		case 6:
			return
		default:
			panic("passive invalid control step")
		}
		req := memcontrolprotocol.Req{MsgMeta: messaging.MsgMeta{ID: timing.GetIDGenerator().Generate(), Src: c.port.AsRemote(), Dst: c.dst, TrafficClass: "memcontrolprotocol.Req"}, Command: cmd}
		if cmd == memcontrolprotocol.CmdFlush || cmd == memcontrolprotocol.CmdInvalidate {
			req.Addresses = p.addresses(t)
		}
		t.pending[req.ID] = req.Dst
		t.queue = append(t.queue, req)
	}
}

func (p *Passive) prepareTransfer(t *passiveTransition) {
	s := p.s
	st := s.pages[t.page]
	t.event = PassiveEvent{Kernel: p.kernel, Page: t.page, GPU: t.gpu, Request: t.req.Meta().ID, StartPS: uint64(t.start), Version: st.Version, Kind: "write-revoke"}
	t.due = p.comp.CurrentTime()
	if t.write {
		return
	}
	// All completed home writes have now been flushed to canonical storage.
	p.writing[t.page] = false
	if t.gpu == int(st.Home.DeviceID) {
		t.event.Kind = "home-read-after-write"
		return
	}
	c := st.Copies[t.gpu]
	if c == nil {
		c = &copyState{Valid: map[uint32]bool{}, SectorVersion: map[uint32]uint64{}}
		st.Copies[t.gpu] = c
	}
	if c.Frame == 0 {
		// The transition drained every request and invalidated old physical tags.
		// A bounded cache evicts only after flushing/invalidation in this transition.
		if !p.makeRoom(t.gpu) {
			panic("passive cannot allocate replica capacity")
		}
		c.Frame = s.d.AllocateReplicaFrame(t.gpu)
	}
	t.cold = !c.Had
	t.need = map[uint32]bool{}
	if !c.Had || s.Config.Policy == "inv" {
		for x := uint32(0); uint64(x) < s.Config.PageBytes/s.Config.SectorBytes; x++ {
			t.need[x] = true
		}
	} else {
		for x := range t.bits {
			if !c.Valid[x] {
				t.need[x] = true
			}
		}
		if s.Config.Policy == "retain" {
			t.need = s.allStale(c)
		} else {
			demand := s.expandRepair(t.need)
			full := s.expandRepair(s.allStale(c))
			predicted := map[uint32]bool{}
			for x := range p.history[[2]uint64{t.page, uint64(t.gpu)}] {
				if !c.Valid[x] && !demand[x] {
					predicted[x] = true
				}
			}
			if len(predicted) > 0 && s.repairServicePS(full) < s.repairServicePS(demand)+s.repairServicePS(s.expandRepair(predicted)) {
				s.Stats.Passive.FullStaleRepairs++
				s.Stats.Passive.ExtraRepairBytes += uint64(len(full)-len(demand)) * s.Config.SectorBytes
				t.need = full
			} else {
				s.Stats.Passive.DemandRepairs++
				t.need = demand
			}
		}
	}
	t.need = s.expandRepair(t.need)
	t.event.Kind = "repair"
	if !c.Had {
		t.event.Kind = "cold-copy"
	}
	t.event.Bytes = uint64(len(t.need)) * s.Config.SectorBytes
	now := p.comp.CurrentTime()
	t.serviceStart = now
	ready := now + timing.VTimeInPicoSec(s.Config.FaultNS)*1000
	t.due = ready
	xs := sorted(t.need)
	for lo := 0; lo < len(xs); {
		hi := lo + 1
		for hi < len(xs) && xs[hi] == xs[hi-1]+1 {
			hi++
		}
		t.due = max(t.due, s.Fabric.Reserve(gpuName(int(st.Home.DeviceID)), gpuName(t.gpu), uint64(hi-lo)*s.Config.SectorBytes+16, ready))
		lo = hi
	}
	s.Stats.Passive.CopyServicePS += uint64(t.due - now)
}

func (p *Passive) finishTransfer(t *passiveTransition) {
	if t.write || t.gpu == int(p.s.pages[t.page].Home.DeviceID) {
		return
	}
	s := p.s
	st := s.pages[t.page]
	c := st.Copies[t.gpu]
	for x := range t.need {
		off := uint64(x) * s.Config.SectorBytes
		data, err := s.d.Resources().GlobalStorage.Read(st.Home.PAddr+off, s.Config.SectorBytes)
		must(err)
		must(s.d.Resources().GlobalStorage.Write(c.Frame+off, data))
		c.Valid[x] = true
		c.SectorVersion[x] = st.LastWrite[x]
	}
	key := [2]uint64{t.page, uint64(t.gpu)}
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
		p.repaired[key] = true
	}
	p.lastCopyKernel[key] = p.kernel
	s.Stats.RecoveryEvents = append(s.Stats.RecoveryEvents, RecoveryEvent{HomeGPU: int(st.Home.DeviceID), Role: "runtime", RepairKind: s.Config.Policy, Epoch: p.kernel, Page: t.page, GPU: t.gpu, Version: st.Version, Cold: t.cold, Bytes: t.event.Bytes, ChargedBytes: t.event.Bytes, StartPS: uint64(t.serviceStart), DuePS: uint64(t.due)})
	c.Had = true
	c.Readable = true
	s.clock++
	c.Stamp = s.clock
	s.snapshot()
}

func (p *Passive) makeRoom(gpu int) bool {
	limit := p.s.Config.ReplicaPagesPerGPU
	if limit == 0 {
		return true
	}
	count := 0
	var victim *copyState
	for _, st := range p.s.pages {
		if c := st.Copies[gpu]; c != nil && c.Frame != 0 {
			count++
			if victim == nil || c.Stamp < victim.Stamp || (c.Stamp == victim.Stamp && c.Frame < victim.Frame) {
				victim = c
			}
		}
	}
	if count < limit {
		return true
	}
	// Eviction needs its own physical-cache invalidation, beyond the current page.
	// Refuse it rather than recycle a frame with live stale cache tags.
	return false
}

func (p *Passive) HostWrite(page uint64, bits map[uint32]bool) { p.markWrite(page, 0, bits) }
func (p *Passive) Finish() {
	if p.next != nil || len(p.leases) > 0 || p.granted != 0 {
		panic("kernel-internal protocol did not drain")
	}
	for _, at := range p.ats {
		addresstranslator.SetAccessController(at, nil)
	}
}
