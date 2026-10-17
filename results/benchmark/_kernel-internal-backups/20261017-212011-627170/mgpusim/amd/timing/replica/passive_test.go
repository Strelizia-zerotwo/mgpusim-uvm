package replica

import (
	"bytes"
	"fmt"
	"github.com/sarchlab/akita/v5/mem"
	"github.com/sarchlab/akita/v5/mem/memprotocol"
	"github.com/sarchlab/akita/v5/mem/vm"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/timing"
	"testing"
)

type idealFixture struct {
	p        *Passive
	now      timing.VTimeInPicoSec
	jobs     []*idealTransfer
	released []uint64
	next     uint64
}

func idealTest(policy string) *idealFixture {
	c := DefaultConfig()
	c.Policy = policy
	s := &Session{Config: c, pages: map[uint64]*pageState{}, Fabric: NewFabric(c)}
	p := newIdealPassive(s, mem.NewStorage(1<<28))
	p.pid = 1
	f := &idealFixture{p: p, next: 1 << 20}
	p.now = func() timing.VTimeInPicoSec { return f.now }
	p.schedule = func(j *idealTransfer) { f.jobs = append(f.jobs, j) }
	p.allocate = func(int) (uint64, bool) { v := f.next; f.next += c.PageBytes; return v, true }
	p.release = func(_ int, frame uint64) { f.released = append(f.released, frame) }
	f.page(4096, 1)
	p.BeginKernel(1)
	return f
}
func (f *idealFixture) page(addr uint64, home int) {
	f.p.s.pages[addr] = &pageState{Home: vm.Page{VAddr: addr, PAddr: addr + 65536, DeviceID: uint64(home), PID: 1, PageSize: f.p.s.Config.PageBytes}, Copies: map[int]*copyState{}, LastWrite: map[uint32]uint64{}}
}
func readReq(addr uint64) memprotocol.ReadReq {
	return memprotocol.ReadReq{MsgMeta: messaging.MsgMeta{ID: timing.GetIDGenerator().Generate()}, PID: 1, Address: addr, AccessByteSize: 4}
}
func writeReq(addr uint64, value byte) memprotocol.WriteReq {
	return memprotocol.WriteReq{MsgMeta: messaging.MsgMeta{ID: timing.GetIDGenerator().Generate()}, PID: 1, Address: addr, Data: []byte{value, value, value, value}}
}
func (f *idealFixture) finish(j *idealTransfer) { f.now = max(f.now, j.due); f.p.finishTransfer(j) }
func (f *idealFixture) read(gpu int, addr uint64) []byte {
	r := readReq(addr)
	if !f.p.admit(gpu, r) {
		j := f.p.waiting[r.ID]
		f.finish(j)
		if !f.p.admit(gpu, r) {
			panic("read not ready")
		}
	}
	v := f.p.ReadData(nil, r.ID, make([]byte, 4))
	f.p.Complete(nil, r.ID)
	return v
}
func (f *idealFixture) write(addr uint64, value byte) {
	w := writeReq(addr, value)
	if !f.p.admit(1, w) {
		panic("write blocked")
	}
	f.p.Complete(nil, w.ID)
}

func TestIdealPoliciesWriteWithoutTimeOrControl(t *testing.T) {
	for _, policy := range []string{"inv", "delta", "delta-sub"} {
		t.Run(policy, func(t *testing.T) {
			f := idealTest(policy)
			f.read(2, 4096)
			c := f.p.s.pages[4096].Copies[2]
			frame := c.Frame
			before := f.now
			f.write(4096, 7)
			f.write(4160, 8)
			if f.now != before || len(f.p.transfers) != 0 || c.Readable {
				t.Fatal("write charged synchronization or left readable copy")
			}
			if f.p.s.pages[4096].Version != 2 || f.p.s.pages[4096].LastWrite[2] != 2 {
				t.Fatal("write versions missing")
			}
			if policy == "inv" {
				if c.Frame != 0 || len(f.released) != 1 || f.released[0] != frame {
					t.Fatal("INV did not immediately release logical frame")
				}
			} else if c.Frame != frame || len(f.released) > 0 {
				t.Fatal("delta failed to retain frame")
			}
			if f.p.s.Stats.Invalidations != 1 {
				t.Fatal("multiple writes counted multiple readable-to-invalid transitions")
			}
			if f.p.s.Stats.Passive.CacheControlPS+f.p.s.Stats.Passive.HomeFetchPS+f.p.s.Stats.Passive.MetadataPS+f.p.s.Stats.Passive.InflightWaitPS != 0 {
				t.Fatal("non-copy coherence charged")
			}
		})
	}
}
func TestIdealDeltaRepairsAllAccumulatedStaleSectors(t *testing.T) {
	for _, policy := range []string{"inv", "delta", "delta-sub"} {
		t.Run(policy, func(t *testing.T) {
			f := idealTest(policy)
			f.read(2, 4096)
			f.write(4096, 7)
			f.write(4160, 8)
			before := f.p.s.Stats.CopyBytes
			if got := f.read(2, 4096); !bytes.Equal(got, []byte{7, 7, 7, 7}) {
				t.Fatalf("read %v", got)
			}
			want := uint64(64)
			if policy == "inv" {
				want = 4096
			}
			if f.p.s.Stats.CopyBytes-before != want {
				t.Fatalf("repair bytes %d want %d", f.p.s.Stats.CopyBytes-before, want)
			}
			before = f.p.s.Stats.Faults
			f.read(2, 4160)
			if f.p.s.Stats.Faults != before {
				t.Fatal("all-stale repair left second stale sector")
			}
			if f.p.s.Stats.Passive.StaleReadSectors != 0 {
				t.Fatal("stale readable data")
			}
		})
	}
}
func TestIdealSubblockCleanReadAvoidsEntireService(t *testing.T) {
	for _, policy := range []string{"delta", "delta-sub"} {
		f := idealTest(policy)
		f.read(2, 4096)
		f.write(4096, 7)
		before := f.p.s.Stats.Faults
		f.read(2, 4128)
		want := uint64(1)
		if policy == "delta-sub" {
			want = 0
		}
		if f.p.s.Stats.Faults-before != want {
			t.Fatal(policy, "incorrect clean-sector service")
		}
		if policy == "delta-sub" && f.p.s.Stats.Passive.CleanReplicaReads != 1 {
			t.Fatal("clean skip not reported")
		}
	}
}
func TestIdealReadSnapshotSurvivesWriteDuringFault(t *testing.T) {
	for _, policy := range []string{"inv", "delta", "delta-sub"} {
		t.Run(policy, func(t *testing.T) {
			f := idealTest(policy)
			r := readReq(4096)
			if f.p.admit(2, r) {
				t.Fatal("cold fault missing")
			}
			j := f.p.waiting[r.ID]
			frame := j.frame
			f.write(4096, 9)
			if len(f.released) > 0 && policy == "inv" {
				t.Fatal("backend frame reused before snapshot read completes")
			}
			if policy == "inv" && (f.p.s.pages[4096].Copies[2].Frame != 0 || f.p.retired[frame] != 2) {
				t.Fatal("INV logical release did not separate backend pin")
			}
			f.finish(j)
			if !f.p.admit(2, r) {
				t.Fatal("old read not granted")
			}
			if got := f.p.ReadData(nil, r.ID, []byte{9, 9, 9, 9}); !bytes.Equal(got, make([]byte, 4)) {
				t.Fatal("waiting read lost its snapshot")
			}
			f.p.Complete(nil, r.ID)
			if got := f.read(2, 4096); !bytes.Equal(got, []byte{9, 9, 9, 9}) {
				t.Fatal("future read did not see new write")
			}
			f.p.CheckAtBarrier()
			f.p.Finish()
		})
	}
}
func TestIdealIndependentPagesAndSameVersionCoalescing(t *testing.T) {
	f := idealTest("delta")
	f.page(8192, 3)
	a, b, c := readReq(4096), readReq(4096), readReq(8192)
	if f.p.admit(2, a) || f.p.admit(2, b) || f.p.admit(4, c) {
		t.Fatal("unexpected immediate fault completion")
	}
	if len(f.jobs) != 2 || f.p.waiting[a.ID] != f.p.waiting[b.ID] || f.p.s.Stats.Passive.CoalescedReads != 1 {
		t.Fatal("same page/version not coalesced")
	}
	if f.jobs[0].start != f.jobs[1].start || f.jobs[0].due != f.jobs[1].due {
		t.Fatal("disjoint GPU pairs serialized")
	}
	f.finish(f.jobs[1])
	if !f.p.admit(4, c) || f.p.admit(2, a) {
		t.Fatal("unrelated page blocked by first fault")
	}
	f.p.Complete(nil, c.ID)
	f.finish(f.jobs[0])
	for _, r := range []memprotocol.ReadReq{a, b} {
		if !f.p.admit(2, r) {
			t.Fatal("coalesced reader stuck")
		}
		f.p.Complete(nil, r.ID)
	}
}
func TestIdealOldRepairCannotOverwriteNewerVersion(t *testing.T) {
	f := idealTest("delta")
	f.read(2, 4096)
	f.write(4096, 1)
	a := readReq(4096)
	f.p.admit(2, a)
	old := f.p.waiting[a.ID]
	f.write(4096, 2)
	b := readReq(4096)
	f.p.admit(2, b)
	newer := f.p.waiting[b.ID]
	f.finish(newer)
	f.finish(old)
	for _, r := range []memprotocol.ReadReq{a, b} {
		if !f.p.admit(2, r) {
			t.Fatal("lost reader")
		}
		f.p.Complete(nil, r.ID)
	}
	if got := f.read(2, 4096); got[0] != 2 {
		t.Fatal("old completion overwrote latest copy")
	}
}
func TestIdealServiceCostsAcrossRequestedScan(t *testing.T) {
	for _, page := range []uint64{4096, 65536, 2097152} {
		for _, fault := range []uint64{900, 20000} {
			for _, link := range []uint64{0, 200} {
				t.Run(fmt.Sprintf("%d-%d-%d", page, fault, link), func(t *testing.T) {
					cost := func(policy string, n uint64) timing.VTimeInPicoSec {
						f := idealTest(policy)
						f.p.s.Config.PageBytes = page
						f.p.s.Config.FaultNS = fault
						f.p.s.Config.LinkNS = link
						f.p.s.Fabric = NewFabric(f.p.s.Config)
						end := f.p.reserve(2, 1, n, 0)
						if policy != "infinity" && (f.p.s.Fabric.Messages != 1 || f.p.s.Fabric.Bytes != n || f.p.s.Stats.Passive.ChargedFixedPS != fault*1000) {
							t.Fatal("extra header, extent or fixed service charge")
						}
						return end
					}
					inv, delta, infinity := cost("inv", page), cost("delta", 32), cost("infinity", page)
					if delta > inv || infinity != 0 {
						t.Fatal("isolated service cost ordering violated")
					}
				})
			}
		}
	}
}
func TestIdealHostWriteResetsFunctionalSource(t *testing.T) {
	f := idealTest("delta")
	f.read(2, 4096)
	f.p.HostWrite(4096, map[uint32]bool{0: true})
	must(f.p.storage.Write(69632, []byte{3, 3, 3, 3}))
	if got := f.read(2, 4096); got[0] != 3 {
		t.Fatal("H2D did not become authoritative")
	}
}
func TestIdealEvictedFrameFallsBackToWholePage(t *testing.T) {
	f := idealTest("delta")
	f.p.s.Config.ReplicaPagesPerGPU = 1
	f.page(8192, 1)
	f.read(2, 4096)
	f.read(2, 8192)
	before := f.p.s.Stats.CopyBytes
	f.read(2, 4096)
	if f.p.s.Stats.CopyBytes-before != 4096 || f.p.s.Stats.Passive.FullFallbacks != 1 {
		t.Fatal("reclaimed old frame did not use full-page fallback")
	}
}
func TestIdealInfinityDoesNotIntroduceAnAdmissionTick(t *testing.T) {
	f := idealTest("infinity")
	r := readReq(4096)
	if !f.p.admit(2, r) || len(f.jobs) != 0 || f.p.s.Stats.Passive.ChargedBytes != 0 {
		t.Fatal("free copy stalled")
	}
	f.p.Complete(nil, r.ID)
}
func TestIdealKernelLabelDoesNotRepairOrInvalidate(t *testing.T) {
	f := idealTest("delta")
	f.read(2, 4096)
	f.write(4096, 1)
	c := *f.p.s.pages[4096].Copies[2]
	n := len(f.jobs)
	f.p.BeginKernel(2)
	after := f.p.s.pages[4096].Copies[2]
	if after.Frame != c.Frame || after.Version != c.Version || after.Readable != c.Readable || len(f.jobs) != n {
		t.Fatal("kernel label changed replica state")
	}
}

func TestIdealNewFrameCannotUseHistoricalValidVersion(t *testing.T) {
	for _, policy := range []string{"delta", "delta-sub"} {
		f := idealTest(policy)
		f.p.s.Config.ReplicaPagesPerGPU = 1
		f.page(8192, 1)
		f.read(2, 4096)
		f.read(2, 8192)
		a, b := readReq(4096), readReq(4128)
		if f.p.admit(2, a) || f.p.admit(2, b) {
			t.Fatal(policy, "uninitialized reclaimed frame became readable")
		}
		if f.p.waiting[a.ID] != f.p.waiting[b.ID] || !f.p.waiting[a.ID].full {
			t.Fatal("same-version full copy not coalesced")
		}
		f.finish(f.p.waiting[a.ID])
		for _, r := range []memprotocol.ReadReq{a, b} {
			if !f.p.admit(2, r) {
				t.Fatal("lost reader")
			}
			f.p.Complete(nil, r.ID)
		}
	}
}
func TestIdealWriteDirtyMaskAndReadFallback(t *testing.T) {
	f := idealTest("delta")
	f.read(2, 4096)
	w := writeReq(4126, 4)
	w.DirtyMask = []bool{true, true, false, false}
	if !f.p.admit(1, w) {
		t.Fatal("masked write stalled")
	}
	f.p.Complete(nil, w.ID)
	st := f.p.s.pages[4096]
	if st.LastWrite[0] != 1 || st.LastWrite[1] != 0 {
		t.Fatal("unwritten sector dirtied")
	}
	f.read(2, 4096)
	if f.p.s.Stats.Passive.RepairBytes != 32 {
		t.Fatal("masked write charged extra sector")
	}
	f.page(8192, 1)
	f.p.allocate = func(int) (uint64, bool) { return 0, false }
	r := readReq(8192)
	if !f.p.admit(2, r) || f.p.s.Stats.Passive.FallbackReads != 1 {
		t.Fatal("out-of-memory fallback blocked")
	}
	f.p.Complete(nil, r.ID)
}
