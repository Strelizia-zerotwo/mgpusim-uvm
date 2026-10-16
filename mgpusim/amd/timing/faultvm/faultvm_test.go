package faultvm

import (
	"testing"

	"github.com/sarchlab/akita/v5/hooking"
	"github.com/sarchlab/akita/v5/mem/vm"
	"github.com/sarchlab/akita/v5/mem/vm/vmprotocol"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/modeling"
	"github.com/sarchlab/akita/v5/timing"
	"github.com/sarchlab/mgpusim/v5/amd/timing/localmapping"
)

type noopConnection struct{ hooking.HookableBase }

func (c *noopConnection) Name() string                     { return "test" }
func (c *noopConnection) PlugIn(p messaging.Port)          { p.SetConnection(c) }
func (c *noopConnection) Unplug(_ messaging.Port)          {}
func (c *noopConnection) NotifyAvailable(_ messaging.Port) {}
func (c *noopConnection) NotifySend()                      {}

type fixture struct {
	tables    *localmapping.Tables
	host      *Host
	gpus      []*GPU
	now       timing.VTimeInPicoSec
	responses map[uint64]vm.Page
}

func testConfig() Config {
	c := DefaultConfig()
	c.LocalWalkCycles = 2
	c.LocalWalkers = 2
	c.HostWalkCycles = 20
	c.HostWalkers = 1
	c.HostQueueSize = 1
	c.LinkCycles = 3
	c.InstallCycles = 2
	return c
}

func makeFixture(config Config) *fixture {
	f := &fixture{tables: localmapping.NewDemandLocal(12, 2), responses: make(map[uint64]vm.Page)}
	f.tables.UseFixedPlacement()
	reg := modeling.NewStandaloneRegistrar(timing.NewSerialEngine())
	f.host = BuildHost(reg, "Host", config, f.tables)
	(&noopConnection{}).PlugIn(f.host.GetPortByName("Top"))
	for _, name := range []string{"GPU1", "GPU2"} {
		spec := MakeGPUSpec(config, f.host.GetPortByName("Top").AsRemote(), 12)
		gpu := BuildGPU(reg, name, spec, f.tables.View(len(f.gpus)))
		(&noopConnection{}).PlugIn(gpu.GetPortByName("Top"))
		(&noopConnection{}).PlugIn(gpu.GetPortByName("Host"))
		f.gpus = append(f.gpus, gpu)
	}
	return f
}

func testPage(pid vm.PID, index uint64) vm.Page {
	return vm.Page{PID: pid, VAddr: index * 4096, PAddr: (index + 1024*uint64(pid)) * 4096,
		PageSize: 4096, Valid: true, Unified: true, DeviceID: 1}
}

func (f *fixture) submit(gpu int, pid vm.PID, addr uint64) uint64 {
	p := f.gpus[gpu].GetPortByName("Top")
	if !p.CanDeliver() {
		panic("test input overflow")
	}
	req := vmprotocol.TranslationReq{MsgMeta: messaging.MsgMeta{ID: timing.GetIDGenerator().Generate(),
		Src: "Test.Top", Dst: p.AsRemote(), TrafficClass: "vmprotocol.TranslationReq"}, PID: pid, VAddr: addr}
	p.Deliver(req)
	return req.ID
}

func transfer(src, dst messaging.Port) {
	if msg := src.PeekOutgoing(); msg != nil && dst.CanDeliver() {
		dst.Deliver(msg)
		src.RetrieveOutgoing()
	}
}

func (f *fixture) tick() {
	for _, gpu := range f.gpus {
		gpu.Middlewares()[0].(*gpuMiddleware).step(f.now)
		transfer(gpu.GetPortByName("Host"), f.host.GetPortByName("Top"))
		port := gpu.GetPortByName("Top")
		for msg := port.RetrieveOutgoing(); msg != nil; msg = port.RetrieveOutgoing() {
			rsp := msg.(vmprotocol.TranslationRsp)
			if _, exists := f.responses[rsp.RspTo]; exists {
				panic("duplicate response")
			}
			f.responses[rsp.RspTo] = rsp.Page
		}
	}
	f.host.Middlewares()[0].(*hostMiddleware).step(f.now)
	port := f.host.GetPortByName("Top")
	if msg := port.PeekOutgoing(); msg != nil {
		for _, gpu := range f.gpus {
			if gpu.GetPortByName("Host").AsRemote() == msg.Meta().Dst {
				transfer(port, gpu.GetPortByName("Host"))
				break
			}
		}
	}
	f.now += Cycle
}

func (f *fixture) run(t *testing.T, expected int) {
	t.Helper()
	for i := 0; i < 10000 && len(f.responses) < expected; i++ {
		f.tick()
	}
	if len(f.responses) != expected {
		t.Fatalf("lost response/deadlock: got %d, want %d", len(f.responses), expected)
	}
	f.tick() // Retire the final empty transaction after draining its output.
}

func TestFaultCoalescingAndWarmLocalHit(t *testing.T) {
	f := makeFixture(testConfig())
	page := testPage(1, 1)
	f.tables.Insert(page)
	first := f.submit(0, 1, 4096)
	second := f.submit(0, 1, 4128)
	f.run(t, 2)
	if f.responses[first] != page || f.responses[second] != page {
		t.Fatal("wrong returned physical mapping")
	}
	stats := f.gpus[0].State.Stats
	if stats.LocalFaults != 1 || stats.Coalesced != 1 || f.host.State.Stats.Received != 1 {
		t.Fatalf("same page was not coalesced: %+v", stats)
	}
	third := f.submit(0, 1, 4096)
	f.run(t, 3)
	if f.responses[third] != page || f.gpus[0].State.Stats.LocalHits != 1 || f.host.State.Stats.Received != 1 {
		t.Fatal("warm access faulted again or returned wrong data")
	}
	f.submit(1, 1, 4096)
	f.run(t, 4)
	if f.host.State.Stats.Received != 2 {
		t.Fatal("one GPU's fill incorrectly populated another GPU")
	}
}

func TestOrdinaryAllocationsUseLocalWalkWithoutHostRequests(t *testing.T) {
	c := testConfig()
	f := makeFixture(c)
	page := testPage(1, 1)
	page.Unified = false
	f.tables.Insert(page)
	f.submit(0, 1, 4096)
	f.submit(1, 1, 4096)
	f.run(t, 2)
	for _, gpu := range f.gpus {
		s := gpu.State.Stats
		if s.LocalHits != 1 || s.LocalFaults != 0 || s.LatencySum != duration(c.LocalWalkCycles) {
			t.Fatalf("ordinary allocation bypassed/changed local walk: %+v", s)
		}
	}
	if f.host.State.Stats.Received != 0 {
		t.Fatal("ordinary allocation contacted host")
	}
}

func runContention(t *testing.T, walkers int) *fixture {
	t.Helper()
	c := testConfig()
	c.HostWalkers = walkers
	f := makeFixture(c)
	for index := uint64(1); index <= 8; index++ {
		f.tables.Insert(testPage(1, index))
		f.submit(int(index%2), 1, index*4096)
	}
	f.run(t, 8)
	s := f.host.State.Stats
	if s.Received != 8 || s.Completed != 8 || s.QueueHighWater > 1 || s.ActiveHighWater > uint64(walkers) {
		t.Fatalf("capacity exceeded or requests lost: %+v", s)
	}
	if s.ServiceSum != 8*duration(c.HostWalkCycles) {
		t.Fatal("backpressure changed service accounting")
	}
	return f
}

func TestFiniteHostQueueBackpressuresAndWalkersChangeLatency(t *testing.T) {
	slow := runContention(t, 1)
	fast := runContention(t, 4)
	if slow.host.State.Stats.QueueFullCycles == 0 || slow.host.State.Stats.QueueWaitSum == 0 {
		t.Fatal("finite queue did not cause measurable waiting")
	}
	if slow.now <= fast.now {
		t.Fatalf("extra walkers did not reduce makespan: slow=%d fast=%d", slow.now, fast.now)
	}
}

func TestPIDIsolationAndOrdinaryAllocations(t *testing.T) {
	f := makeFixture(testConfig())
	a, b := testPage(1, 1), testPage(2, 1)
	f.tables.Insert(a)
	f.tables.Insert(b)
	idA := f.submit(0, 1, 4096)
	idB := f.submit(0, 2, 4096)
	f.run(t, 2)
	if f.responses[idA] != a || f.responses[idB] != b {
		t.Fatal("PID mappings aliased")
	}
	ordinary := testPage(1, 2)
	ordinary.Unified = false
	f.tables.Insert(ordinary)
	f.submit(1, 1, 8192)
	f.run(t, 3)
	if f.host.State.Stats.Received != 2 {
		t.Fatal("ordinary allocation should be preinstalled")
	}
}

func expectPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected explicit failure")
		}
	}()
	fn()
}

func TestUnknownAddressFailsInsteadOfInventingMapping(t *testing.T) {
	f := makeFixture(testConfig())
	f.submit(0, 1, 4096)
	expectPanic(t, func() { f.run(t, 1) })
}

func TestStaleInstallAndLiveRemapAreRejected(t *testing.T) {
	f := makeFixture(testConfig())
	page := testPage(1, 1)
	f.tables.Insert(page)
	stale := page
	stale.PAddr += 4096
	expectPanic(t, func() { f.tables.View(0).Install(stale) })
	f.submit(0, 1, 4096)
	f.run(t, 1)
	expectPanic(t, func() { f.tables.Update(stale) })
	expectPanic(t, func() { f.tables.Remove(1, 4096) })
}

func TestConfigurationRejectsInvalidResourceCounts(t *testing.T) {
	c := testConfig()
	c.HostWalkers = 0
	expectPanic(t, c.Validate)
}
