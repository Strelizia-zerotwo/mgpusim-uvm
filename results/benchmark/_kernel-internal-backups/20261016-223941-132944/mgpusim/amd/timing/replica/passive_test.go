package replica

import (
	"github.com/sarchlab/akita/v5/mem/vm"
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
	return &Passive{s: s, writer: map[uint64]int{}, writing: map[uint64]bool{}, history: map[[2]uint64]map[uint32]bool{}, currentReads: map[[2]uint64]map[uint32]bool{}, repaired: map[[2]uint64]bool{}}
}

func TestPassiveEveryWriteRevokesRepairedSector(t *testing.T) {
	for _, policy := range []string{"retain", "i1-adaptive"} {
		t.Run(policy, func(t *testing.T) {
			p := testPassive(policy)
			bits := map[uint32]bool{0: true}
			c := p.s.pages[4096].Copies[2]
			p.markWrite(4096, 1, bits)
			if p.valid(c, bits) {
				t.Fatal("first write left a readable stale copy")
			}
			// A completed demand repair happens while the SAME kernel is still live.
			c.Valid[0] = true
			c.Readable = true
			p.repaired[[2]uint64{4096, 2}] = true
			if !p.mustTransition(4096, 1, bits, true) {
				t.Fatal("subsequent writer bypassed repaired-copy revocation")
			}
			p.markWrite(4096, 1, bits)
			if p.valid(c, bits) || p.s.Stats.Passive.WriteAfterRepair != 1 {
				t.Fatal("repair-then-write lost invalidation")
			}
		})
	}
}

func TestPassiveCleanSubblockSurvivesDisjointWrite(t *testing.T) {
	p := testPassive("i1-adaptive")
	p.markWrite(4096, 1, map[uint32]bool{0: true})
	c := p.s.pages[4096].Copies[2]
	if !p.valid(c, map[uint32]bool{4: true}) {
		t.Fatal("clean existing subblock lost S3 permission")
	}
	if p.valid(c, map[uint32]bool{0: true}) {
		t.Fatal("dirty subblock incorrectly readable")
	}
	if p.mustTransition(4096, 2, map[uint32]bool{4: true}, false) {
		t.Fatal("clean existing read should not need a repair")
	}
}

func TestPassiveRetainRevokesWholeReadPermission(t *testing.T) {
	p := testPassive("retain")
	p.markWrite(4096, 1, map[uint32]bool{0: true})
	if p.valid(p.s.pages[4096].Copies[2], map[uint32]bool{4: true}) {
		t.Fatal("retain incorrectly allowed partial local read")
	}
}

func TestOnlineAdaptiveUsesPastFootprintAndCanChooseBoth(t *testing.T) {
	p := testPassive("i1-adaptive")
	c := p.s.pages[4096].Copies[2]
	delete(c.Valid, 0)
	delete(c.Valid, 4)
	demand := map[uint32]bool{0: true}
	need, full := p.onlineRepair(4096, 2, c, demand)
	if full || len(need) != 1 {
		t.Fatal("no history must use demand repair")
	}
	p.history[[2]uint64{4096, 2}] = map[uint32]bool{0: true, 4: true}
	need, full = p.onlineRepair(4096, 2, c, demand)
	if !full || len(need) != 2 {
		t.Fatal("recurrent second read should amortize a full-stale repair")
	}
	if c.Valid[0] || c.Valid[4] {
		t.Fatal("prediction itself must not publish valid data")
	}
}
