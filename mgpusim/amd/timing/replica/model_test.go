package replica

import (
	"github.com/sarchlab/akita/v5/mem/vm"
	"github.com/sarchlab/akita/v5/timing"
	"testing"
)

func panics(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("expected rejection")
		}
	}()
	f()
}
func TestPageLocalFallbackAndMalformedPlan(t *testing.T) {
	p := &Plan{Format: 1, PageBytes: 4096, SectorBytes: 32, Epochs: []Epoch{{Accesses: []Access{
		{Page: 4096, GPU: 1, Writes: []uint32{0}}, {Page: 4096, GPU: 2, Reads: []uint32{0}},
		{Page: 8192, GPU: 1, Writes: []uint32{0}}, {Page: 8192, GPU: 2, Writes: []uint32{1}},
		{Page: 12288, GPU: 1, Writes: []uint32{0}}, {Page: 12288, GPU: 2, Reads: []uint32{1}},
	}}}}
	p.Validate()
	uses := classify(p.Epochs[0])
	if !uses[4096].ReadWriteConflict || !uses[8192].MultiWriter || uses[12288].fallback() {
		t.Fatal(uses)
	}
	if len(uses[8192].Dirty) != 2 {
		t.Fatal("multi-writer dirty union lost")
	}
	p.Epochs[0].Accesses[0].Writes = []uint32{128}
	panics(t, p.Validate)
}
func TestStrictS3RejectsColdWriteEpochReplica(t *testing.T) {
	a := Access{Page: 4096, GPU: 2, Reads: []uint32{1}}
	st := &pageState{Home: vm.Page{DeviceID: 1}, Copies: map[int]*copyState{}, LastWrite: map[uint32]uint64{}}
	s := &Session{Config: DefaultConfig(), uses: map[uint64]*PageUse{4096: {Writers: map[int]bool{1: true}}},
		existedAtStart: map[[2]uint64]bool{}}
	s.Config.Policy = "i1"
	if s.canReplicate(st, a, true) {
		t.Fatal("strict S3 admitted a cold copy")
	}
	s.Config.Policy = "i1-write-prefetch"
	if !s.canReplicate(st, a, true) {
		t.Fatal("separate write-prefetch variant missing")
	}
	s.Config.Policy = "i1"
	s.existedAtStart[[2]uint64{4096, 2}] = true
	st.Copies[2] = &copyState{Frame: 8192, Had: true, Valid: map[uint32]bool{1: true}, SectorVersion: map[uint32]uint64{}}
	if !s.canReplicate(st, a, true) {
		t.Fatal("existing clean copy should remain readable")
	}
	delete(st.Copies[2].Valid, 1)
	if s.canReplicate(st, a, true) {
		t.Fatal("strict S3 silently repaired during write epoch")
	}
	s.Config.Policy = "i1-write-prefetch"
	if s.canReplicate(st, a, true) {
		t.Fatal("cold-copy variant also changed stale-existing repair policy")
	}
}
func TestFiniteFabricContentionAndSensitivity(t *testing.T) {
	c := DefaultConfig()
	c.BandwidthGBs = 1
	c.LinkNS = 5
	f := NewFabric(c)
	a := f.Reserve("GPU[1].RDMA.a", "GPU[2]", 100, 0)
	b := f.Reserve("GPU[1]", "GPU[3]", 100, 0)
	d := f.Reserve("GPU[4]", "GPU[5]", 100, 0)
	if a != 105000 || b != 205000 || d != a {
		t.Fatalf("contention not modeled: %d %d %d", a, b, d)
	}
	c.BandwidthGBs = 0
	g := NewFabric(c)
	if v := g.Reserve("GPU[1]", "GPU[2]", 1<<20, 0); v != 5000 {
		t.Fatal(v)
	}
}
func TestTransferGranularityAndExtents(t *testing.T) {
	m := map[uint32]bool{0: true, 1: true, 4: true}
	a := intervals(m, 32, 32)
	b := intervals(m, 32, 128)
	if a.Bytes != 96 || a.Extents != 2 || b.Bytes != 256 || b.Extents != 1 {
		t.Fatalf("bad ranges: %+v %+v", a, b)
	}
}
func TestSectorGuardRejectsStaleAndUnplannedReads(t *testing.T) {
	s := &Session{Config: DefaultConfig(), active: &Epoch{Accesses: []Access{{Page: 4096, GPU: 2, Reads: []uint32{0, 1}}}},
		mapped: map[[2]uint64]bool{{4096, 2}: true},
		pages: map[uint64]*pageState{4096: {Copies: map[int]*copyState{
			2: {Frame: 8192, Valid: map[uint32]bool{0: true}},
		}}},
	}
	s.guard(4096, 2, map[uint32]bool{0: true}, false)
	panics(t, func() { s.guard(4096, 2, map[uint32]bool{1: true}, false) })
	panics(t, func() { s.guard(4096, 2, map[uint32]bool{2: true}, false) })
}
func TestFaultInfinityDoesNotMakeFabricFree(t *testing.T) {
	c := DefaultConfig()
	c.FaultNS = 0
	f := NewFabric(c)
	if f.Reserve("GPU[1]", "GPU[2]", 4096, timing.VTimeInPicoSec(1000)) <= 1000 {
		t.Fatal("zero fault must preserve transfer time")
	}
}

func TestS3ShadowDoesNotResurrectEvictedReplica(t *testing.T) {
	c := DefaultConfig()
	c.ReplicaPagesPerGPU = 1
	s := &Session{Config: c, shadow: map[[2]uint64]bool{}, baselineValid: map[[2]uint64]bool{},
		pages: map[uint64]*pageState{4096: {Home: vm.Page{DeviceID: 1}}, 8192: {Home: vm.Page{DeviceID: 1}}}}
	e := Epoch{Accesses: []Access{{Page: 4096, GPU: 2, Reads: []uint32{1}}}}
	s.prepareShadow(e, map[uint64]int{})
	e.Accesses[0].Page = 8192
	s.prepareShadow(e, map[uint64]int{})
	if s.shadow[[2]uint64{4096, 2}] {
		t.Fatal("old copy should have been evicted")
	}
	e.Accesses[0].Page = 4096
	s.baselineValid = map[[2]uint64]bool{}
	s.prepareShadow(e, map[uint64]int{4096: 1})
	if s.baselineValid[[2]uint64{4096, 2}] || s.Stats.S4Targets != 0 {
		t.Fatal("evicted copy counted as S3/S4 target")
	}
}

func TestFallbackCommitKeepsAllWritersInOneVersion(t *testing.T) {
	e := Epoch{Accesses: []Access{{Page: 4096, GPU: 1, Writes: []uint32{0}},
		{Page: 4096, GPU: 2, Writes: []uint32{1}}}}
	c := &copyState{Frame: 8192, Had: true, Valid: map[uint32]bool{0: true, 1: true, 2: true}}
	st := &pageState{Version: 3, Home: vm.Page{VAddr: 4096, DeviceID: 1}, LastWrite: map[uint32]uint64{},
		Copies: map[int]*copyState{3: c}}
	cfg := DefaultConfig()
	cfg.Policy = "i1"
	s := &Session{Config: cfg, uses: classify(e), pages: map[uint64]*pageState{4096: st}}
	s.commit(e)
	if st.Version != 4 || st.LastWrite[0] != 4 || st.LastWrite[1] != 4 {
		t.Fatal("writer union/version lost", st)
	}
	if c.Valid[0] || c.Valid[1] || !c.Valid[2] {
		t.Fatal("wrong invalidated subblocks", c.Valid)
	}
}

func TestRepairLookaheadAvoidsRepeatedServiceForDisjointRereads(t *testing.T) {
	c := &copyState{Had: true, Valid: map[uint32]bool{}}
	for x := uint32(2); x < 128; x++ {
		c.Valid[x] = true
	}
	a := Access{Page: 4096, GPU: 2, Reads: []uint32{0}}
	s := &Session{Config: DefaultConfig(), Plan: &Plan{Epochs: []Epoch{{Accesses: []Access{a}},
		{Accesses: []Access{{Page: 4096, GPU: 2, Reads: []uint32{1}}}}}}}
	s.Config.Policy = "i1"
	if len(s.required(c, a)) != 1 {
		t.Fatal("demand policy changed")
	}
	s.Config.Policy = "i1-full-stale"
	if len(s.required(c, a)) != 2 {
		t.Fatal("full stale did not include later-needed sector")
	}
	s.Config.Policy = "i1-adaptive"
	if len(s.required(c, a)) != 2 {
		t.Fatal("lookahead failed to avoid a second fault")
	}
	s.Plan.Epochs = s.Plan.Epochs[:1]
	if len(s.required(c, a)) != 1 {
		t.Fatal("lookahead copied unread stale sectors without a benefit")
	}
	c.Valid[0] = true
	if len(s.required(c, a)) != 0 {
		t.Fatal("clean-subblock hit triggered unnecessary repair")
	}
}
