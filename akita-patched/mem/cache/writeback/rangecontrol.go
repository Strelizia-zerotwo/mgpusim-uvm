package writeback

import (
	"github.com/sarchlab/akita/v5/mem/memcontrolprotocol"
	"github.com/sarchlab/akita/v5/mem/memprotocol"
)

// handleInvalidateCleanRange never pauses the cache or discards dirty data.
// The requester holds the range admission lock until the response. Waiting for
// active transactions AND locked blocks prevents an old fill after the ack.
func (m *ctrlMiddleware) handleInvalidateCleanRange(req memcontrolprotocol.Req) bool {
	if len(req.Addresses) == 0 {
		panic("clean-range invalidate requires addresses")
	}
	state := &m.pipeline.comp.State
	size := uint64(1) << m.pipeline.comp.Spec().Log2BlockSize
	addresses := map[uint64]bool{}
	for _, a := range req.Addresses {
		addresses[a/size*size] = true
	}
	match := func(a uint64) bool { return addresses[a/size*size] }
	if r, ok := m.pipeline.topPort().PeekIncoming().(memprotocol.AccessReq); ok && match(r.GetAddress()) {
		return true
	}
	for i := range state.Transactions {
		t := &state.Transactions[i]
		if !t.Removed && ((t.HasRead && match(t.ReadAddress)) || (t.HasWrite && match(t.WriteAddress))) {
			return true
		}
	}
	for _, set := range state.DirectoryState.Sets {
		for _, b := range set.Blocks {
			if !match(b.Tag) || (req.PID != 0 && uint64(b.PID) != uint64(req.PID)) {
				continue
			}
			if b.IsLocked || b.ReadCount > 0 {
				return true
			}
			if b.IsValid && b.IsDirty {
				panic("clean-range invalidation of dirty data")
			}
		}
	}
	if !m.ctrlPort().CanSend() {
		return false
	}
	invalidateBlocks(state, size, req.Addresses, req.PID)
	m.ctrlPort().Send(makeCtrlRsp(m.ctrlPort(), req.Command, req.Src, req.ID, true, ""))
	m.ctrlPort().RetrieveIncoming()
	return true
}
