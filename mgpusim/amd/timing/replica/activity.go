package replica

import "github.com/sarchlab/akita/v5/tracing"

// EpochActivity records actual CU instruction lifetimes. It distinguishes
// enqueued kernels from GPUs that really had instructions in flight together.
type EpochActivity struct {
	Name              string         `json:"name"`
	StartPS           uint64         `json:"start_ps"`
	EndPS             uint64         `json:"end_ps"`
	Instructions      map[int]uint64 `json:"instructions_by_gpu"`
	ConcurrentPS      map[int]uint64 `json:"inflight_gpu_duration_ps"`
	MaxConcurrentGPUs int            `json:"max_gpus_with_inflight_instructions"`
}
type activityTracer struct {
	tracing.NopTracer
	session *Session
	gpu     int
	insts   map[uint64]bool
}

func (t *activityTracer) StartTask(task tracing.TaskStart) {
	s := t.session
	if task.Kind != "inst" || s.epochActivity == nil {
		return
	}
	s.advanceActivity(uint64(task.Time))
	t.insts[task.ID] = true
	s.activityCounts[t.gpu]++
	s.epochActivity.Instructions[t.gpu]++
	s.epochActivity.MaxConcurrentGPUs = max(s.epochActivity.MaxConcurrentGPUs, s.activeGPUs())
}
func (t *activityTracer) EndTask(task tracing.TaskEnd) {
	if !t.insts[task.ID] {
		return
	}
	delete(t.insts, task.ID)
	s := t.session
	if s.epochActivity == nil {
		panic("replica: instruction escaped explicit epoch")
	}
	s.advanceActivity(uint64(task.Time))
	s.activityCounts[t.gpu]--
}
func (s *Session) activeGPUs() int {
	n := 0
	for _, count := range s.activityCounts {
		if count > 0 {
			n++
		}
	}
	return n
}
func (s *Session) advanceActivity(now uint64) {
	if now < s.activityLast {
		panic("replica: instruction clock moved backwards")
	}
	s.epochActivity.ConcurrentPS[s.activeGPUs()] += now - s.activityLast
	s.activityLast = now
}

func (s *Session) beginActivity(name string, start uint64) {
	s.activityLast = start
	s.activityCounts = map[int]int{}
	s.epochActivity = &EpochActivity{Name: name, StartPS: start,
		Instructions: map[int]uint64{}, ConcurrentPS: map[int]uint64{}}
}
func (s *Session) endActivity(end uint64) {
	s.advanceActivity(end)
	if s.activeGPUs() != 0 {
		panic("replica: kernel returned with instructions in flight")
	}
	s.epochActivity.EndPS = end
	s.Stats.Activity = append(s.Stats.Activity, *s.epochActivity)
	s.epochActivity = nil
}
