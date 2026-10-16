package runner

import (
	"github.com/sarchlab/akita/v5/mem/vm/mmu"
	"github.com/sarchlab/akita/v5/simulation"
	"github.com/sarchlab/akita/v5/tracing"
	"github.com/sarchlab/mgpusim/v5/amd/timing/localmapping"
)

type mmuTracer struct {
	comp  *mmu.Comp
	table *localmapping.View
	walks *tracing.AverageTimeTracer
}

func (r *reporter) injectMMUTracers(s *simulation.Simulation) {
	if !*reportAll && !*mmuReportFlag {
		return
	}
	for _, comp := range s.Components() {
		mmuComp, ok := comp.(*mmu.Comp)
		if !ok {
			continue
		}
		table, ok := mmuComp.Resources().PageTable.(*localmapping.View)
		if !ok {
			continue
		}
		tracer := tracing.NewAverageTimeTracer(func(task tracing.TaskStart) bool {
			return task.Kind == "req_in" && task.What == "TranslationReq"
		})
		tracing.CollectTrace(mmuComp, tracer)
		r.mmuTracers = append(r.mmuTracers, &mmuTracer{comp: mmuComp, table: table, walks: tracer})
	}
}

func (r *reporter) reportMMU() {
	for _, t := range r.mmuTracers {
		stats := t.table.Snapshot()
		rows := []metric{
			{What: "mmu_ptw_configured_cycles", Value: float64(t.comp.Spec().Latency), Unit: "cycles"},
			{What: "mmu_frequency_hz", Value: float64(t.comp.Spec().Freq), Unit: "Hz"},
			{What: "mmu_max_requests_in_flight", Value: float64(t.comp.Spec().MaxRequestsInFlight), Unit: "count"},
			{What: "mmu_page_size", Value: float64(uint64(1) << t.table.GetLog2PageSize()), Unit: "byte"},
			{What: "mmu_ptw_completed", Value: float64(t.walks.TotalCount()), Unit: "count"},
			{What: "mmu_translation_average_latency", Value: secondsOf(t.walks.AverageTime()), Unit: "second"},
			{What: "mmu_pt_lookup_count", Value: float64(stats.Lookups), Unit: "count"},
			{What: "mmu_pt_hit_count", Value: float64(stats.Hits), Unit: "count"},
			{What: "mmu_pt_missing_count", Value: float64(stats.Missing), Unit: "count"},
			{What: "mmu_pt_invalid_count", Value: float64(stats.Invalid), Unit: "count"},
			{What: "mmu_pt_migrating_count", Value: float64(stats.Migrating), Unit: "count"},
		}
		for _, row := range rows {
			row.Location = t.comp.Name()
			r.dataRecorder.InsertData(tableName, row)
		}
	}
}
