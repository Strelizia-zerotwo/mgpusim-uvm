package replica

import (
	"github.com/sarchlab/akita/v5/mem"
	"github.com/sarchlab/akita/v5/mem/memcontrolprotocol"
	"github.com/sarchlab/akita/v5/mem/vm"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/modeling"
	"github.com/sarchlab/akita/v5/timing"
	"github.com/sarchlab/mgpusim/v5/amd/timing/rdma"
	"testing"
)

func testPassive(policy string) *Passive {
	c := DefaultConfig()
	c.Policy = policy
	st := &pageState{Home: vm.Page{VAddr: 4096, PAddr: 8192, DeviceID: 1}, LastWrite: map[uint32]uint64{}, Copies: map[int]*copyState{}}
	copy := &copyState{Frame: 12288, Had: true, Readable: true, Valid: map[uint32]bool{}, SectorVersion: map[uint32]uint64{}}
	for x := uint32(0); x < 128; x++ {
		copy.Valid[x] = true
	}
	st.Copies[2] = copy
	s := &Session{Config: c, pages: map[uint64]*pageState{4096: st}, Stats: Stats{Passive: &PassiveStats{}}}
	return &Passive{s: s, writers: map[[2]uint64]map[uint32]bool{}, writing: map[uint64]bool{}, history: map[[2]uint64]map[uint32]bool{}, currentReads: map[[2]uint64]map[uint32]bool{}, readVersion: map[[2]uint64]uint64{}, repaired: map[uint64]bool{}, homeSeen: map[[2]uint64]map[uint32]uint64{}, leases: map[uint64]passiveLease{}}
}
func TestPassiveWriteOnlyUpdatesDirectory(t *testing.T) {
	for _, policy := range []string{"inv", "retain", "i1-adaptive"} {
		t.Run(policy, func(t *testing.T) {
			p := testPassive(policy)
			c := p.s.pages[4096].Copies[2]
			bits := map[uint32]bool{0: true}
			p.markWrite(4096, 1, bits)
			if p.valid(4096, c, bits) {
				t.Fatal("stale sector permitted")
			}
			if !c.Valid[0] || c.Frame != 12288 || !c.Readable {
				t.Fatal("write mutated a remote copy")
			}
			c.SectorVersion[0] = p.s.pages[4096].LastWrite[0]
			c.Version = p.s.pages[4096].Version
			if !p.valid(4096, c, bits) {
				t.Fatal("repaired data not permitted")
			}
			p.repaired[4096] = true
			p.markWrite(4096, 1, bits)
			if p.mustTransition(4096, 1, bits, true) || p.s.Stats.Passive.WriteTransitions != 0 {
				t.Fatal("write triggered maintenance")
			}
			if p.valid(4096, c, bits) || p.s.Stats.Passive.WriteAfterRepair != 1 {
				t.Fatal("repair then write lost version mismatch")
			}
		})
	}
}
func TestPassivePartialRepairCannotHideOtherStaleSector(t *testing.T) {
	p := testPassive("i1-adaptive")
	c := p.s.pages[4096].Copies[2]
	p.markWrite(4096, 1, map[uint32]bool{0: true, 4: true})
	c.SectorVersion[0] = p.s.pages[4096].LastWrite[0]
	c.Version = p.s.pages[4096].Version // even an accidentally advanced page version cannot hide sector 4
	if !p.valid(4096, c, map[uint32]bool{0: true}) || p.valid(4096, c, map[uint32]bool{4: true}) {
		t.Fatal("partial repair lost per-sector validity")
	}
	if !p.valid(4096, c, map[uint32]bool{8: true}) {
		t.Fatal("clean subblock lost S3")
	}
}
func TestPassiveRetainAndInvRevalidateWholePage(t *testing.T) {
	for _, policy := range []string{"retain", "inv"} {
		p := testPassive(policy)
		p.markWrite(4096, 1, map[uint32]bool{0: true})
		if p.valid(4096, p.s.pages[4096].Copies[2], map[uint32]bool{4: true}) {
			t.Fatal("whole page policy allowed stale page version")
		}
	}
}
func TestOnlineAdaptiveUsesPastFootprintAndCanChooseBoth(t *testing.T) {
	p := testPassive("i1-adaptive")
	c := p.s.pages[4096].Copies[2]
	p.markWrite(4096, 1, map[uint32]bool{0: true, 4: true})
	demand := map[uint32]bool{0: true}
	need, full := p.onlineRepair(4096, 2, c, demand)
	if full || len(need) != 1 {
		t.Fatal("no history must use demand")
	}
	p.history[[2]uint64{4096, 2}] = map[uint32]bool{0: true, 4: true}
	need, full = p.onlineRepair(4096, 2, c, demand)
	if !full || len(need) != 2 {
		t.Fatal("past recurrent read should amortize full repair")
	}
	if c.SectorVersion[0] != 0 || c.SectorVersion[4] != 0 {
		t.Fatal("prediction changed versions")
	}
}
func TestPassiveRejectsUnsupportedPolicy(t *testing.T) {
	for _, policy := range []string{"norep", "i1", "i1-no-s3", "i1-full-stale", "infinity", "i2"} {
		t.Run(policy, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("unsupported passive policy accepted")
				}
			}()
			validatePassivePolicy(policy)
		})
	}
}
func TestPassivePageWaitDoesNotWaitForOtherPages(t *testing.T) {
	p := testPassive("inv")
	p.leases[1] = passiveLease{managed: true, virtual: 8192, page: vm.Page{PAddr: 16384}}
	if p.pageBusy(4096) {
		t.Fatal("unrelated page blocked repair")
	}
	if !p.pageBusy(8192) || !p.frameBusy(16384) {
		t.Fatal("inflight range not protected")
	}
}

func TestAnnotatedSystemScopeWrittenPageUsesHome(t *testing.T) {
	p := testPassive("i1-adaptive")
	p.s.Config.HasSystemScopeSync = true
	if p.useHome(4096, 2, map[uint32]bool{4: true}) {
		t.Fatal("unwritten page unnecessarily disabled replication")
	}
	p.markWrite(4096, 1, map[uint32]bool{0: true})
	if !p.useHome(4096, 2, map[uint32]bool{4: true}) {
		t.Fatal("annotated written page must conservatively use home")
	}
}

func TestProtocolRequestsUsePhysicalCacheNamespace(t *testing.T) {
	p := testPassive("retain")
	engine := timing.NewSerialEngine()
	p.comp = modeling.NewBuilder[struct{}, struct{}, modeling.None]().WithEngine(engine).WithFreq(timing.GHz).Build("ReplicaTest")
	port := messaging.NewPort(p.comp, 64, 64, "ReplicaTest.Fetch")
	dst := messaging.RemotePort("HomeL2.Top")
	module := modeling.NewBuilder[rdma.Spec, rdma.State, rdma.Resources]().WithEngine(engine).WithFreq(timing.GHz).WithResources(rdma.Resources{LocalModules: &mem.SinglePortMapper{Port: dst}}).Build("HomeRDMA")
	p.homeModules = map[int]*rdma.Comp{1: module}
	p.readPorts = map[messaging.RemotePort]messaging.Port{dst: port}
	p.fetches = map[uint64]passiveFetch{}
	job := &passiveTransition{page: 4096, gpu: 2, need: map[uint32]bool{0: true}, frame: 12288, pending: map[uint64]messaging.RemotePort{}}
	p.beginFetch(job)
	if len(job.fetchQueue) != 1 || job.fetchQueue[0].PID != 0 || job.fetchQueue[0].Address != 8192 {
		t.Fatal("home fetch must share AT physical PID=0 namespace")
	}
	p.caches = []passiveCache{{name: "GPU[2].L2", gpu: 2, l2: true, port: port, dst: dst}}
	p.issueInvalidation(job)
	if len(job.queue) != 1 || job.queue[0].PID != 0 || job.queue[0].Command != memcontrolprotocol.CmdInvalidateCleanRange {
		t.Fatal("invalidation must reach physical cache tags")
	}
}
