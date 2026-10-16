package rdma

import (
	"github.com/sarchlab/akita/v5/mem/memprotocol"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/timing"
	"testing"
)

type delayedFabric struct{}

func (delayedFabric) Reserve(_, _ string, _ uint64, now timing.VTimeInPicoSec) timing.VTimeInPicoSec {
	return now + 100000
}

func TestFabricAllowsConcurrentFlightsAndBackpressuresAtCapacity(t *testing.T) {
	c, m, out, _ := buildTracedRDMA(t)
	SetFabric(c, delayedFabric{})
	in := c.GetPortByName("RDMARequestInside")
	for i := 0; i < c.Spec().BufferSize; i++ {
		in.Deliver(memprotocol.ReadReq{MsgMeta: messaging.MsgMeta{ID: timing.GetIDGenerator().Generate(),
			Src: "L1", Dst: in.AsRemote()}, Address: 0x100, AccessByteSize: 64})
		if !m.processFromL1() {
			t.Fatal("propagation incorrectly blocked subsequent injection", i)
		}
	}
	if len(m.flights["RDMARequestOutside"]) != c.Spec().BufferSize {
		t.Fatal("missing simultaneous flights")
	}
	in.Deliver(memprotocol.ReadReq{MsgMeta: messaging.MsgMeta{ID: timing.GetIDGenerator().Generate(),
		Src: "L1", Dst: in.AsRemote()}, Address: 0x100, AccessByteSize: 64})
	if m.processFromL1() || in.PeekIncoming() == nil {
		t.Fatal("finite flight queue failed to backpressure")
	}
	m.sendFlights("RDMARequestOutside", 1, 99999)
	if out.PeekOutgoing() != nil {
		t.Fatal("message arrived before propagation delay")
	}
	m.sendFlights("RDMARequestOutside", 1, 100000)
	if out.RetrieveOutgoing() == nil || !m.canSendOutside("RDMARequestOutside") {
		t.Fatal("due flight did not free a slot")
	}
}
func TestFabricDrainWaitsForDelayedResponse(t *testing.T) {
	c, m, _, _ := buildTracedRDMA(t)
	SetFabric(c, delayedFabric{})
	outside := c.GetPortByName("RDMADataOutside")
	inside := c.GetPortByName("RDMADataInside")
	outside.Deliver(memprotocol.ReadReq{MsgMeta: messaging.MsgMeta{ID: timing.GetIDGenerator().Generate(),
		Src: "GPU[2].RDMA.RDMARequestOutside", Dst: outside.AsRemote()}, Address: 0x100, AccessByteSize: 64})
	if !m.processIncomingReq() {
		t.Fatal("request admission failed")
	}
	forwarded := inside.RetrieveOutgoing()
	inside.Deliver(memprotocol.DataReadyRsp{MsgMeta: messaging.MsgMeta{ID: timing.GetIDGenerator().Generate(),
		RspTo: forwarded.Meta().ID, Src: "LocalCache", Dst: inside.AsRemote()}, Data: make([]byte, 64)})
	if !m.processFromL2() {
		t.Fatal("response admission failed")
	}
	ctrl := &ctrlMiddleware{comp: c}
	if ctrl.fullyDrained() {
		t.Fatal("drain completed with a response still in flight")
	}
	m.sendFlights("RDMADataOutside", 1, 100000)
	if outside.RetrieveOutgoing() == nil || !ctrl.fullyDrained() {
		t.Fatal("response did not complete drain")
	}
}

func TestReadyDestinationsBypassDelayedFlowsWithoutReorderingOneFlow(t *testing.T) {
	mk := func(dst string, due timing.VTimeInPicoSec) fabricFlight {
		return fabricFlight{Due: due, Msg: memprotocol.ReadReq{MsgMeta: messaging.MsgMeta{Dst: messaging.RemotePort(dst)}}}
	}
	q := []fabricFlight{mk("busy", 200), mk("busy", 50), mk("free", 100), mk("other", 100)}
	if got := readyFlight(q, 100); got != 2 {
		t.Fatal("ready flow remained head blocked or busy flow reordered", got)
	}
	if got := readyFlight(q[:2], 100); got != -1 {
		t.Fatal("same-destination FIFO violated", got)
	}
	if got := readyFlight(q, 200); got != 2 {
		t.Fatal("earliest ready flow not chosen", got)
	}
}
