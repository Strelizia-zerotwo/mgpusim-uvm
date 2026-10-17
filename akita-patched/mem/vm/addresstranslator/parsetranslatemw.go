package addresstranslator

import (
	"github.com/sarchlab/akita/v5/mem/memcontrolprotocol"
	"github.com/sarchlab/akita/v5/mem/memprotocol"
	"github.com/sarchlab/akita/v5/mem/vm/vmprotocol"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/modeling"
	"github.com/sarchlab/akita/v5/timing"
	"github.com/sarchlab/akita/v5/tracing"
)

// parseTranslateMW handles incoming requests from topPort and initiates
// address translation. Control-port handling lives in ctrlMiddleware.
type parseTranslateMW struct {
	comp *modeling.Component[Spec, State, Resources]
}

func (m *parseTranslateMW) topPort() messaging.Port {
	return m.comp.GetPortByName("Top")
}

func (m *parseTranslateMW) translationPort() messaging.Port {
	return m.comp.GetPortByName("Translation")
}

// Tick runs translate while the component is enabled. Pause, Drain, and
// Reset all stop new translation work; in-flight transactions continue
// to drain through respondPipelineMW until the component is fully
// paused.
func (m *parseTranslateMW) Tick() bool {
	madeProgress := false

	if m.comp.State.ControlState == memcontrolprotocol.StateEnabled {
		spec := m.comp.Spec()
		for range spec.NumReqPerCycle {
			madeProgress = m.translate() || madeProgress
		}
	}

	return madeProgress
}

func (m *parseTranslateMW) translate() bool {
	if !m.translationPort().CanSend() {
		return false
	}
	var itemI messaging.Msg
	deferredIndex := -1
	controller := accessController(m.comp)
	// Ready deferred requests can bypass waiting requests to other pages.
	for i, st := range m.comp.State.DeferredRequests {
		req := restoreDeferredAccess(st)
		if controller.Admit(m.comp, req) {
			itemI = req
			deferredIndex = i
			break
		}
	}
	madeProgress := false
	if itemI == nil {
		for m.topPort().PeekIncoming() != nil {
			candidate := m.topPort().PeekIncoming().(memprotocol.AccessReq)
			if controller == nil || controller.Admit(m.comp, candidate) {
				itemI = candidate
				break
			}
			deferred, ok := controller.(DeferredAccessController)
			if !ok || !deferred.DeferBlockedAccess() {
				return madeProgress
			}
			m.comp.State.DeferredRequests = append(m.comp.State.DeferredRequests, msgToIncomingReqState(candidate))
			m.topPort().RetrieveIncoming()
			madeProgress = true
		}
	}
	if itemI == nil {
		return madeProgress
	}
	item := itemI.(memprotocol.AccessReq)
	vAddr := item.GetAddress()
	spec := m.comp.Spec()
	vPageID := addrToPageID(vAddr, spec.Log2PageSize)

	transReq := vmprotocol.TranslationReq{}
	transReq.ID = timing.GetIDGenerator().Generate()
	transReq.Src = m.translationPort().AsRemote()
	transReq.Dst = m.comp.Resources().TranslationProviderMapper.Find(vAddr)
	transReq.PID = item.GetPID()
	transReq.VAddr = vPageID
	transReq.DeviceID = spec.DeviceID
	transReq.TrafficClass = "vmprotocol.TranslationReq"

	if !m.translationPort().CanSend() {
		return false
	}

	m.translationPort().Send(transReq)

	// The translation request is on its way: the at-head wait to send it on the
	// Translation port — counted on the incoming-buffer task since the request
	// reached the head of the Top buffer — is over. The req_in opened just below
	// starts at retrieve and covers only the processing that follows.
	tracing.AddMilestone(m.comp, tracing.Milestone{
		TaskID: tracing.MsgIDAtIncomingBuffer(itemI, m.comp),
		Kind:   tracing.MilestoneKindNetworkBusy,
		What:   m.translationPort().Name(),
	})

	incoming := msgToIncomingReqState(itemI)

	nextState := &m.comp.State

	tracing.TraceReqReceive(m.comp, itemI)
	tracing.TraceReqInitiate(
		m.comp,
		transReq,
		tracing.MsgIDAtReceiver(itemI, m.comp),
	)

	trans := transactionState{
		IncomingReqs:      []incomingReqState{incoming},
		TranslationReqID:  transReq.ID,
		TranslationReqSrc: transReq.Src,
		TranslationReqDst: transReq.Dst,
	}
	nextState.Transactions = append(nextState.Transactions, trans)

	if deferredIndex >= 0 {
		nextState.DeferredRequests = append(nextState.DeferredRequests[:deferredIndex], nextState.DeferredRequests[deferredIndex+1:]...)
	} else {
		m.topPort().RetrieveIncoming()
	}

	return true
}

func restoreDeferredAccess(st incomingReqState) memprotocol.AccessReq {
	meta := messaging.MsgMeta{ID: st.ID, Src: st.Src, Dst: st.Dst, RspTo: st.RspTo}
	if st.Type == "memprotocol.WriteReq" {
		return memprotocol.WriteReq{MsgMeta: meta, Address: st.Address, Data: st.Data, DirtyMask: st.DirtyMask, PID: st.PID, CanWaitForCoalesce: st.CanWaitForCoalesce}
	}
	return memprotocol.ReadReq{MsgMeta: meta, Address: st.Address, AccessByteSize: st.AccessByteSize, PID: st.PID, CanWaitForCoalesce: st.CanWaitForCoalesce}
}
