package runner

import (
	"github.com/sarchlab/akita/v5/simulation"
	"github.com/sarchlab/akita/v5/timing"
	"github.com/sarchlab/mgpusim/v5/amd/timing/faultvm"
)

type faultMetrics struct {
	gpus  []*faultvm.GPU
	hosts []*faultvm.Host
}

func (r *reporter) injectFaultMetrics(s *simulation.Simulation) {
	r.faultMetrics = &faultMetrics{}
	for _, comp := range s.Components() {
		switch c := comp.(type) {
		case *faultvm.GPU:
			r.faultMetrics.gpus = append(r.faultMetrics.gpus, c)
		case *faultvm.Host:
			r.faultMetrics.hosts = append(r.faultMetrics.hosts, c)
		}
	}
}

func averageSeconds(sum timing.VTimeInPicoSec, count uint64) float64 {
	if count == 0 {
		return 0
	}
	return secondsOf(sum) / float64(count)
}

func (r *reporter) faultMetric(location, name string, value float64, unit string) {
	r.dataRecorder.InsertData(tableName, metric{Location: location, What: "vm_" + name, Value: value, Unit: unit})
}

func (r *reporter) reportFaultMetrics() {
	if r.faultMetrics == nil {
		return
	}
	for _, gpu := range r.faultMetrics.gpus {
		r.reportFaultGPU(gpu)
	}
	for _, host := range r.faultMetrics.hosts {
		r.reportFaultHost(host)
	}
}

func (r *reporter) reportFaultGPU(g *faultvm.GPU) {
	s := g.State.Stats
	config := g.Spec().Config()
	counts := map[string]uint64{
		"requests": s.Requests, "completed": s.Completed, "coalesced": s.Coalesced,
		"local_walks": s.Walks, "local_hits": s.LocalHits, "local_faults": s.LocalFaults,
		"fault_requests": s.FaultSent, "fault_resolved": s.FaultResolved,
		"outstanding_peak": s.OutstandingHighWater, "outstanding_end": uint64(len(g.State.Transactions)),
		"local_walkers": uint64(config.LocalWalkers), "transaction_slots": uint64(config.MaxTransactions),
		"max_waiters": uint64(config.MaxWaiters),
	}
	for name, value := range counts {
		r.faultMetric(g.Name(), name, float64(value), "count")
	}
	r.faultMetric(g.Name(), "page_size", float64(uint64(1)<<g.Spec().Log2PageSize), "byte")
	r.faultMetric(g.Name(), "frequency_hz", 1e9, "Hz")
	cycles := map[string]int{"local_walk_cycles": config.LocalWalkCycles, "link_cycles": config.LinkCycles,
		"install_cycles": config.InstallCycles}
	for name, value := range cycles {
		r.faultMetric(g.Name(), name, float64(value), "cycles")
	}
	r.faultMetric(g.Name(), "admission_blocked_cycles", float64(s.AdmissionBlockedCycles), "cycles")
	r.faultMetric(g.Name(), "host_send_blocked_cycles", float64(s.HostSendBlockedCycles), "cycles")
	r.faultMetric(g.Name(), "translation_average", averageSeconds(s.LatencySum, s.Completed), "second")
	r.faultMetric(g.Name(), "translation_max", secondsOf(s.LatencyMax), "second")
	r.faultMetric(g.Name(), "fault_average", averageSeconds(s.FaultLatencySum, s.FaultResolved), "second")
	r.faultMetric(g.Name(), "fault_max", secondsOf(s.FaultLatencyMax), "second")
	r.faultMetric(g.Name(), "local_queue_average", averageSeconds(s.LocalQueueWaitSum, s.Walks), "second")
}

func (r *reporter) reportFaultHost(h *faultvm.Host) {
	s := h.State.Stats
	config := h.Spec()
	counts := map[string]uint64{"received": s.Received, "completed": s.Completed,
		"queue_peak": s.QueueHighWater, "active_peak": s.ActiveHighWater,
		"queue_end": uint64(len(h.State.Queue)), "active_end": uint64(len(h.State.Active)),
		"walkers": uint64(config.HostWalkers), "queue_capacity": uint64(config.HostQueueSize)}
	for name, value := range counts {
		r.faultMetric(h.Name(), name, float64(value), "count")
	}
	r.faultMetric(h.Name(), "frequency_hz", 1e9, "Hz")
	r.faultMetric(h.Name(), "service_cycles", float64(config.HostWalkCycles), "cycles")
	r.faultMetric(h.Name(), "queue_full_cycles", float64(s.QueueFullCycles), "cycles")
	r.faultMetric(h.Name(), "response_blocked_cycles", float64(s.ResponseBlockedCycles), "cycles")
	r.faultMetric(h.Name(), "queue_wait_average", averageSeconds(s.QueueWaitSum, s.Completed), "second")
	r.faultMetric(h.Name(), "queue_wait_max", secondsOf(s.QueueWaitMax), "second")
	r.faultMetric(h.Name(), "ingress_wait_average", averageSeconds(s.IngressWaitSum, s.Received), "second")
	r.faultMetric(h.Name(), "service_average", averageSeconds(s.ServiceSum, s.Completed), "second")
}
