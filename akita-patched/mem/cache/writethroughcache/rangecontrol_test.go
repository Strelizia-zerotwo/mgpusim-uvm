package writethroughcache

import (
	"github.com/sarchlab/akita/v5/mem"
	"github.com/sarchlab/akita/v5/mem/memcontrolprotocol"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/modeling"
	"github.com/sarchlab/akita/v5/timing"
	"testing"
)

func rangeTestCache() (*Comp, *ctrlMiddleware, messaging.Port) {
	spec := DefaultSpec()
	spec.TotalByteSize = 65536
	spec.Log2BlockSize = 6
	comp := MakeBuilder().WithRegistrar(modeling.NewStandaloneRegistrar(timing.NewSerialEngine())).WithSpec(spec).WithResources(Resources{Storage: mem.NewStorage(1 << 20)}).Build("RangeCache")
	for _, name := range []string{"Top", "Bottom", "Control"} {
		port := messaging.NewPort(comp, 16, 16, comp.Name()+"."+name)
		comp.AssignPort(name, port)
		(&ccNoopConn{}).PlugIn(port)
	}
	return comp, &ctrlMiddleware{pipeline: &pipelineMW{comp: comp}}, comp.GetPortByName("Control")
}
func TestRunningRangePreservesOtherPagesAndWaitsForTarget(t *testing.T) {
	comp, m, port := rangeTestCache()
	for i := 0; i < 2; i++ {
		b := &comp.State.DirectoryState.Sets[i].Blocks[0]
		b.Tag = uint64(i) * 64
		b.IsValid = true
	}
	req := memcontrolprotocol.Req{MsgMeta: messaging.MsgMeta{ID: 501, Src: "Agent", Dst: port.AsRemote()}, Command: memcontrolprotocol.CmdInvalidateCleanRange, Addresses: []uint64{0}}
	port.Deliver(req)
	// The target miss has not returned. No acknowledgement may pass it.
	comp.State.Transactions = []transactionState{{HasRead: true, ReadAddress: 0}, {HasRead: true, ReadAddress: 64}}
	m.handleInvalidateCleanRange(req)
	if port.RetrieveOutgoing() != nil || !comp.State.DirectoryState.Sets[0].Blocks[0].IsValid {
		t.Fatal("invalidated before matching transaction finished")
	}
	comp.State.Transactions[0].Removed = true
	m.handleInvalidateCleanRange(req)
	rsp, ok := port.RetrieveOutgoing().(memcontrolprotocol.Rsp)
	if !ok || !rsp.Success || rsp.RspTo != 501 {
		t.Fatal("unrelated transaction blocked range acknowledgement")
	}
	if comp.State.DirectoryState.Sets[0].Blocks[0].IsValid || !comp.State.DirectoryState.Sets[1].Blocks[0].IsValid {
		t.Fatal("range did not preserve unrelated hot line")
	}
	if comp.State.IsPaused || comp.State.IsDraining {
		t.Fatal("range command paused cache")
	}
}
