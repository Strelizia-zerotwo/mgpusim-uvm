package replica

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Only the simulation thread publishes samples. The monitor reads immutable
// copies, never maps/counters owned by the engine, and never calls TickLater.
type progressSample struct {
	Kernel           int    `json:"kernel"`
	KernelInProgress bool   `json:"kernel_in_progress"`
	SimulationPS     uint64 `json:"simulation_ps"`
	ManagedRequests  uint64 `json:"managed_requests"`
	Copies           uint64 `json:"copy_events"`
	CopyBytes        uint64 `json:"copy_bytes"`
	Leases           int    `json:"leases"`
	Transfers        int    `json:"transfers"`
	Waiting          int    `json:"waiting_reads"`
}
type progressRecord struct {
	progressSample
	Kind          string `json:"kind"`
	WallTime      string `json:"wall_time"`
	EngineRunning bool   `json:"engine_running"`
	HeapAlloc     uint64 `json:"heap_alloc_bytes"`
	HeapSys       uint64 `json:"heap_sys_bytes"`
	NumGC         uint32 `json:"num_gc"`
	Goroutines    int    `json:"goroutines"`
}
type progressMonitor struct {
	sample        atomic.Pointer[progressSample]
	file          *os.File
	path          string
	engineRunning func() bool
	stop, done    chan struct{}
	mu            sync.Mutex
	last          progressSample
	idleSamples   int
	dumped        bool
}

func (p *Passive) EnableProgress(path string, interval time.Duration) error {
	if path == "" || interval <= 0 {
		return nil
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	m := &progressMonitor{file: file, path: path, engineRunning: p.s.d.EngineRunning, stop: make(chan struct{}), done: make(chan struct{})}
	p.progress = m
	m.sample.Store(&progressSample{})
	go func() {
		defer close(m.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.record("periodic")
			case <-m.stop:
				return
			}
		}
	}()
	return nil
}

func (p *Passive) publishProgress(kind string, active bool) {
	m := p.progress
	if m == nil {
		return
	}
	s := &progressSample{Kernel: p.kernel, KernelInProgress: active, SimulationPS: uint64(p.now()),
		ManagedRequests: p.s.Stats.Passive.ManagedRequests, Copies: p.s.Stats.Faults, CopyBytes: p.s.Stats.CopyBytes,
		Leases: len(p.leases), Transfers: len(p.transfers), Waiting: len(p.waiting)}
	m.sample.Store(s)
	if kind != "" {
		m.record(kind)
	}
}
func (m *progressMonitor) record(kind string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := *m.sample.Load()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	r := progressRecord{progressSample: s, Kind: kind, WallTime: time.Now().Format(time.RFC3339), EngineRunning: m.engineRunning(),
		HeapAlloc: memory.HeapAlloc, HeapSys: memory.HeapSys, NumGC: memory.NumGC, Goroutines: runtime.NumGoroutine()}
	if err := json.NewEncoder(m.file).Encode(r); err != nil {
		fmt.Fprintf(os.Stderr, "CORE_PROGRESS write failed: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "CORE_PROGRESS %s kernel=%d active=%t sim_us=%.3f requests=%d engine=%t heap_mib=%.1f\n", kind, s.Kernel, s.KernelInProgress, float64(s.SimulationPS)/1e6, s.ManagedRequests, r.EngineRunning, float64(memory.HeapAlloc)/(1<<20))
	// Diagnostic only: capture a native Go stack after two unchanged periodic
	// samples with an active kernel and stopped engine. Do not kill/restart it.
	if kind == "periodic" && s.KernelInProgress && !r.EngineRunning && s == m.last {
		m.idleSamples++
	} else {
		m.idleSamples = 0
		m.dumped = false
	}
	m.last = s
	if m.idleSamples >= 2 && !m.dumped {
		buffer := make([]byte, 2<<20)
		n := runtime.Stack(buffer, true)
		if err := os.WriteFile(m.path+".stacks.txt", buffer[:n], 0644); err != nil {
			fmt.Fprintf(os.Stderr, "CORE_PROGRESS stack write failed: %v\n", err)
		}
		m.dumped = true
		fmt.Fprintln(os.Stderr, "CORE_PROGRESS idle engine with an unchanged active kernel; saved native stacks")
	}
}
func (p *Passive) closeProgress() {
	if p.progress != nil {
		close(p.progress.stop)
		<-p.progress.done
		p.progress.file.Close()
	}
}
