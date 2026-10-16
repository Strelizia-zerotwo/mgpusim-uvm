package faultvm

import (
	"fmt"

	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/timing"
)

type hostMiddleware struct{ comp *Host }

func (m *hostMiddleware) Tick() bool { return m.step(m.comp.CurrentTime()) }

func (m *hostMiddleware) step(now timing.VTimeInPicoSec) bool {
	state := &m.comp.State
	active := state.Active[:0]
	for i := range state.Active {
		job := &state.Active[i]
		if !m.finish(job, now) {
			active = append(active, *job)
		}
	}
	state.Active = active
	m.accept(now)
	m.start(now)
	return len(state.Active) > 0 || len(state.Queue) > 0 || m.comp.GetPortByName("Top").PeekIncoming() != nil
}

func (m *hostMiddleware) accept(now timing.VTimeInPicoSec) {
	port := m.comp.GetPortByName("Top")
	msg := port.PeekIncoming()
	if msg == nil {
		return
	}
	state := &m.comp.State
	if len(state.Queue) >= m.comp.Spec().HostQueueSize {
		state.Stats.QueueFullCycles++
		return
	}
	req, ok := msg.(FaultRequest)
	if !ok {
		panic(fmt.Sprintf("faultvm: unexpected host request %T", msg))
	}
	port.RetrieveIncoming()
	state.Queue = append(state.Queue, hostJob{Req: req, Enqueued: now})
	state.Stats.Received++
	state.Stats.IngressWaitSum += now - req.SentAt
	peak(&state.Stats.QueueHighWater, len(state.Queue))
}

func (m *hostMiddleware) start(now timing.VTimeInPicoSec) {
	state := &m.comp.State
	spec := m.comp.Spec()
	for len(state.Queue) > 0 && len(state.Active) < spec.HostWalkers {
		job := state.Queue[0]
		state.Queue = state.Queue[1:]
		job.Started = now
		job.Due = now + duration(spec.HostWalkCycles)
		addLatency(&state.Stats.QueueWaitSum, &state.Stats.QueueWaitMax, now-job.Enqueued)
		state.Active = append(state.Active, job)
		peak(&state.Stats.ActiveHighWater, len(state.Active))
	}
}

func (m *hostMiddleware) finish(job *hostJob, now timing.VTimeInPicoSec) bool {
	if now < job.Due {
		return false
	}
	if !job.Resolved {
		page, found := m.comp.Resources().Table.Find(job.Req.PID, job.Req.VAddr)
		if !found || !page.Valid || page.IsMigrating {
			panic(fmt.Sprintf("faultvm: no usable authoritative allocation for pid=%d vaddr=%#x",
				job.Req.PID, job.Req.VAddr))
		}
		job.Page = page
		job.Resolved = true
		m.comp.State.Stats.ServiceSum += now - job.Started
	}
	port := m.comp.GetPortByName("Top")
	if !port.CanSend() {
		m.comp.State.Stats.ResponseBlockedCycles++
		return false
	}
	rsp := FaultResponse{MsgMeta: messaging.MsgMeta{ID: timing.GetIDGenerator().Generate(),
		Src: port.AsRemote(), Dst: job.Req.Src, RspTo: job.Req.ID,
		TrafficClass: "mappingfault.response"}, Page: job.Page}
	port.Send(rsp)
	m.comp.State.Stats.Completed++
	return true
}
