package faultvm

import (
	"fmt"

	"github.com/sarchlab/akita/v5/mem/vm/vmprotocol"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/timing"
	"github.com/sarchlab/akita/v5/tracing"
)

type gpuMiddleware struct{ comp *GPU }

func (m *gpuMiddleware) Tick() bool { return m.step(m.comp.CurrentTime()) }

func (m *gpuMiddleware) step(now timing.VTimeInPicoSec) bool {
	m.receiveHost(now)
	state := &m.comp.State
	remaining := state.Transactions[:0]
	for i := range state.Transactions {
		tx := &state.Transactions[i]
		m.advance(tx, now)
		if len(tx.Waiters) > 0 {
			remaining = append(remaining, *tx)
		}
	}
	state.Transactions = remaining
	m.accept(now)
	m.startWalks(now)
	return len(state.Transactions) > 0 || m.comp.GetPortByName("Top").PeekIncoming() != nil
}

func (m *gpuMiddleware) accept(now timing.VTimeInPicoSec) {
	port := m.comp.GetPortByName("Top")
	msg := port.PeekIncoming()
	if msg == nil {
		return
	}
	req, ok := msg.(vmprotocol.TranslationReq)
	if !ok {
		panic(fmt.Sprintf("faultvm: unexpected GPU request %T", msg))
	}
	spec := m.comp.Spec()
	addr := (req.VAddr >> spec.Log2PageSize) << spec.Log2PageSize
	state := &m.comp.State
	var target *transaction
	for i := range state.Transactions {
		tx := &state.Transactions[i]
		if tx.PID == req.PID && tx.VAddr == addr {
			target = tx
			break
		}
	}
	if target != nil && len(target.Waiters) >= spec.Config().MaxWaiters ||
		target == nil && len(state.Transactions) >= spec.Config().MaxTransactions {
		state.Stats.AdmissionBlockedCycles++
		return
	}
	port.RetrieveIncoming()
	tracing.TraceReqReceive(m.comp, req)
	state.Stats.Requests++
	if target == nil {
		state.Transactions = append(state.Transactions, transaction{PID: req.PID, VAddr: addr, Enqueued: now})
		target = &state.Transactions[len(state.Transactions)-1]
		peak(&state.Stats.OutstandingHighWater, len(state.Transactions))
	} else {
		state.Stats.Coalesced++
	}
	target.Waiters = append(target.Waiters, waiter{Req: req, Arrived: now})
}

func (m *gpuMiddleware) startWalks(now timing.VTimeInPicoSec) {
	active := 0
	for _, tx := range m.comp.State.Transactions {
		if tx.Stage == walking {
			active++
		}
	}
	config := m.comp.Spec().Config()
	for i := range m.comp.State.Transactions {
		tx := &m.comp.State.Transactions[i]
		if active == config.LocalWalkers {
			break
		}
		if tx.Stage != waitingWalker {
			continue
		}
		tx.Stage = walking
		tx.Due = now + duration(config.LocalWalkCycles)
		m.comp.State.Stats.LocalQueueWaitSum += now - tx.Enqueued
		active++
	}
}

func (m *gpuMiddleware) advance(tx *transaction, now timing.VTimeInPicoSec) {
	if now < tx.Due {
		return
	}
	switch tx.Stage {
	case walking:
		m.finishWalk(tx, now)
	case requestFlight:
		m.sendFault(tx, now)
	case responseFlight:
		tx.Stage = installing
		tx.Due = now + duration(m.comp.Spec().Config().InstallCycles)
	case installing:
		m.comp.Resources().Table.Install(tx.Page)
		tx.Stage = ready
		stats := &m.comp.State.Stats
		stats.FaultResolved++
		addLatency(&stats.FaultLatencySum, &stats.FaultLatencyMax, now-tx.FaultStarted)
	case waitingWalker, waitingHost, ready:
		// These states advance through admission, responses, or send readiness.
	}
	if tx.Stage == ready {
		m.respond(tx, now)
	}
}

func (m *gpuMiddleware) finishWalk(tx *transaction, now timing.VTimeInPicoSec) {
	stats := &m.comp.State.Stats
	stats.Walks++
	page, found := m.comp.Resources().Table.Find(tx.PID, tx.VAddr)
	if found && page.Valid && !page.IsMigrating {
		stats.LocalHits++
		tx.Page = page
		tx.Stage = ready
		return
	}
	if found {
		panic("faultvm: invalid or migrating mapping; live migration is unsupported")
	}
	stats.LocalFaults++
	tx.FaultStarted = now
	tx.Stage = requestFlight
	tx.Due = now + duration(m.comp.Spec().Config().LinkCycles)
}

func (m *gpuMiddleware) sendFault(tx *transaction, now timing.VTimeInPicoSec) {
	port := m.comp.GetPortByName("Host")
	if !port.CanSend() {
		m.comp.State.Stats.HostSendBlockedCycles++
		return
	}
	id := timing.GetIDGenerator().Generate()
	req := FaultRequest{MsgMeta: messaging.MsgMeta{ID: id, Src: port.AsRemote(),
		Dst: m.comp.Spec().Host, TrafficClass: "mappingfault.request"},
		PID: tx.PID, VAddr: tx.VAddr, SentAt: now}
	port.Send(req)
	tx.HostID = id
	tx.Stage = waitingHost
	m.comp.State.Stats.FaultSent++
}

func (m *gpuMiddleware) receiveHost(now timing.VTimeInPicoSec) {
	port := m.comp.GetPortByName("Host")
	msg := port.PeekIncoming()
	if msg == nil {
		return
	}
	rsp, ok := msg.(FaultResponse)
	if !ok {
		panic(fmt.Sprintf("faultvm: unexpected host response %T", msg))
	}
	for i := range m.comp.State.Transactions {
		tx := &m.comp.State.Transactions[i]
		if tx.Stage != waitingHost || tx.HostID != rsp.RspTo {
			continue
		}
		if tx.PID != rsp.Page.PID || tx.VAddr != rsp.Page.VAddr {
			panic("faultvm: response PID/address does not match request")
		}
		port.RetrieveIncoming()
		tx.Page = rsp.Page
		tx.Stage = responseFlight
		tx.Due = now + duration(m.comp.Spec().Config().LinkCycles)
		return
	}
	panic("faultvm: host response has no pending request")
}

func (m *gpuMiddleware) respond(tx *transaction, now timing.VTimeInPicoSec) {
	port := m.comp.GetPortByName("Top")
	for len(tx.Waiters) > 0 && port.CanSend() {
		w := tx.Waiters[0]
		rsp := vmprotocol.TranslationRsp{MsgMeta: messaging.MsgMeta{
			ID: timing.GetIDGenerator().Generate(), Src: port.AsRemote(),
			Dst: w.Req.Src, RspTo: w.Req.ID, TrafficClass: "vmprotocol.TranslationRsp"}, Page: tx.Page}
		port.Send(rsp)
		tracing.TraceReqComplete(m.comp, w.Req)
		stats := &m.comp.State.Stats
		stats.Completed++
		addLatency(&stats.LatencySum, &stats.LatencyMax, now-w.Arrived)
		tx.Waiters = tx.Waiters[1:]
	}
}
