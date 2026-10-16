package rdma

import (
	"github.com/sarchlab/akita/v5/mem/memprotocol"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/timing"
)

// Fabric budgets serialization separately from propagation. Multiple messages
// may be in flight concurrently; waiting for propagation never owns an egress
// injection slot. The local flight queues remain finite and backpressure ports.
type Fabric interface {
	Reserve(src, dst string, bytes uint64, now timing.VTimeInPicoSec) timing.VTimeInPicoSec
}
type fabricFlight struct {
	Msg      messaging.Msg
	Due      timing.VTimeInPicoSec
	Complete *transaction
}

// SetFabric enables the optional oracle fabric before any RDMA work is issued.
// Original demand runs without an attached fabric retain their existing path.
func SetFabric(c *Comp, f Fabric) {
	for _, mw := range c.Middlewares() {
		if m, ok := mw.(*forwardMiddleware); ok {
			m.fabric = f
			m.flights = map[string][]fabricFlight{}
		}
	}
}
func (m *forwardMiddleware) canSendOutside(port string) bool {
	if m.fabric == nil {
		return m.port(port).CanSend()
	}
	return len(m.flights[port]) < m.comp.Spec().BufferSize
}
func messageBytes(msg messaging.Msg) uint64 {
	n := uint64(16)
	switch v := msg.(type) {
	case memprotocol.WriteReq:
		n += uint64(len(v.Data))
	case memprotocol.DataReadyRsp:
		n += uint64(len(v.Data))
	}
	return n
}
func (m *forwardMiddleware) sendOutside(msg messaging.Msg, port string, complete *transaction) {
	if m.fabric == nil {
		m.port(port).Send(msg)
		if complete != nil {
			m.completeOutsideResponse(*complete)
		}
		return
	}
	due := m.fabric.Reserve(string(msg.Meta().Src), string(msg.Meta().Dst), messageBytes(msg), m.comp.CurrentTime())
	m.flights[port] = append(m.flights[port], fabricFlight{Msg: msg, Due: due, Complete: complete})
}
func (m *forwardMiddleware) advanceFabric() bool {
	if m.fabric == nil {
		return false
	}
	spec := m.comp.Spec()
	now := m.comp.CurrentTime()
	m.sendFlights("RDMARequestOutside", spec.OutgoingReqPerCycle, now)
	m.sendFlights("RDMADataOutside", spec.OutgoingRspPerCycle, now)
	// Tick until queued flights become deliverable, including output backpressure.
	return len(m.flights["RDMARequestOutside"])+len(m.flights["RDMADataOutside"]) > 0
}
func (m *forwardMiddleware) sendFlights(name string, width int, now timing.VTimeInPicoSec) {
	q := m.flights[name]
	port := m.port(name)
	for i := 0; i < width && len(q) > 0; i++ {
		if !port.CanSend() {
			break
		}
		index := readyFlight(q, now)
		if index < 0 {
			break
		}
		f := q[index]
		port.Send(f.Msg)
		if f.Complete != nil {
			m.completeOutsideResponse(*f.Complete)
		}
		copy(q[index:], q[index+1:])
		q[len(q)-1] = fabricFlight{}
		q = q[:len(q)-1]
	}
	m.flights[name] = q
}
func (m *forwardMiddleware) completeOutsideResponse(trans transaction) {
	m.endTransaction(trans)
	state := &m.comp.State
	index := findTransactionByRspTo(trans.ForwardedReqID, state.TransactionsFromOutside)
	state.TransactionsFromOutside = append(state.TransactionsFromOutside[:index],
		state.TransactionsFromOutside[index+1:]...)
}

// readyFlight permits ready destinations to bypass a delayed one. Preserve
// FIFO order within each destination flow and deterministic ties between flows.
func readyFlight(q []fabricFlight, now timing.VTimeInPicoSec) int {
	seen := map[messaging.RemotePort]bool{}
	best := -1
	for i, f := range q {
		dst := f.Msg.Meta().Dst
		if seen[dst] {
			continue
		}
		seen[dst] = true
		if f.Due <= now && (best < 0 || f.Due < q[best].Due) {
			best = i
		}
	}
	return best
}
