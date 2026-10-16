// replica is an explicit two-pass kernel-barrier experiment. It does not
// silently turn arbitrary benchmarks into multi-GPU replication workloads.
package main

import (
	"encoding/json"
	"flag"
	"os"

	"github.com/sarchlab/mgpusim/v5/amd/arch"
	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/amdappsdk/floydwarshall"
	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/shoc/stencil2d"
	"github.com/sarchlab/mgpusim/v5/amd/driver"
	"github.com/sarchlab/mgpusim/v5/amd/samples/runner"
	"github.com/sarchlab/mgpusim/v5/amd/timing/replica"
)

var configPath = flag.String("replica-config", "", "JSON policy configuration")
var planPath = flag.String("replica-plan", "", "Recorded pass-one plan; omit to record")
var planOut = flag.String("replica-plan-out", "observed-plan.json", "Actual executed accesses")
var statsOut = flag.String("replica-stats", "replica-stats.json", "Oracle counters and execution time")
var workload = flag.String("workload", "probe",
	"probe, parallel-stencil, fw or stencil (last two rotate single-GPU kernels)")
var homePlacement = flag.String("home-placement", "owner", "Static grid homes: owner or gpu1")
var alignOwnerPages = flag.Bool("align-owner-pages", false, "Align first interior row and partition page boundaries")
var columns = flag.Int("columns", 258, "Columns for the parallel stencil, 64*k+2")
var probeCase = flag.String("probe-case", "standard", "standard, cold-write or fallback")
var size = flag.Int("size", 32, "FW nodes; stencil uses size rows and 66 columns")
var probePages = flag.Int("probe-pages", 1, "Probe pages: 1 or 2 (second page tests eviction)")
var iterations = flag.Int("iterations", 8, "FW passes or stencil iterations")

//nolint:funlen // Sample wires explicit workload choices to a common session.
func main() {
	flag.Parse()
	c := replica.DefaultConfig()
	if *configPath != "" {
		b, e := os.ReadFile(*configPath)
		check(e)
		check(json.Unmarshal(b, &c))
	}
	c.Validate()
	r := new(runner.Runner).Init()
	if !r.Timing || !r.UseUnifiedMemory || r.Parallel || flag.Lookup("vm-mode").Value.String() != "demand" {
		panic("replica experiments require -timing -use-unified-memory and serial event engine")
	}
	var p *replica.Plan
	if *planPath != "" {
		p = replica.Load(*planPath)
	}
	var session *replica.Session
	ready := func(ctx *driver.Context, ptrs []driver.Ptr, sizes []uint64) {
		r.Driver().WaitForEngineIdle()
		session = replica.New(r.Simulation(), r.Driver(), ctx, c, p)
		for i, ptr := range ptrs {
			session.Manage(ptr, sizes[i])
		}
	}
	epoch := func(name string, work func()) { session.Epoch(name, work) }
	switch *workload {
	case "probe":
		b := &probe{r: r, ready: ready, epoch: epoch, pageBytes: c.PageBytes, pages: *probePages, variant: *probeCase}
		r.AddBenchmark(b)
	case "parallel-stencil", "parallel-stencil-overlap":
		if r.ArchType != arch.GCN3 {
			panic("parallel-stencil requires GCN3")
		}
		b := stencil2d.NewParallelBenchmark(r.Driver())
		b.OverlapHalo = *workload == "parallel-stencil-overlap"
		b.HomePlacement = *homePlacement
		b.AlignOwnerPages = *alignOwnerPages
		b.Rows = *size
		b.Cols = *columns
		b.Iterations = *iterations
		b.MemoryReady = ready
		b.Epoch = epoch
		r.AddBenchmark(b)
	case "parallel-fw":
		if r.ArchType != arch.GCN3 {
			panic("parallel FW requires GCN3")
		}
		b := floydwarshall.NewParallelBenchmark(r.Driver())
		b.Arch = r.ArchType
		b.NumNodes = uint32(*size)
		b.NumIterations = uint32(*iterations)
		b.HomePlacement = *homePlacement
		b.MemoryReady = ready
		b.Epoch = epoch
		r.AddBenchmark(b)
	case "fw":
		if *size < 8 || *size%8 != 0 || *iterations < 1 || *iterations > *size {
			panic("FW needs size multiple of 8 and iterations in [1,size]")
		}
		b := floydwarshall.NewBenchmark(r.Driver())
		b.Arch = r.ArchType
		b.NumNodes = uint32(*size)
		b.NumIterations = uint32(*iterations)
		b.MemoryReady = ready
		b.Epoch = epoch
		b.RotateGPUs = true
		r.AddBenchmark(b)
	case "stencil":
		if *size < 18 || (*size-2)%16 != 0 || *iterations < 1 {
			panic("stencil size must be 16*k+2")
		}
		b := stencil2d.NewBenchmark(r.Driver())
		b.Arch = r.ArchType
		b.NumRows = *size
		b.NumCols = 66
		b.NumIteration = *iterations
		b.MemoryReady = ready
		b.Epoch = epoch
		b.RotateGPUs = true
		r.AddBenchmark(b)
	default:
		panic("unsupported replica workload")
	}
	r.Run()
	session.Finish(*planOut, *statsOut)
}
func check(err error) {
	if err != nil {
		panic(err)
	}
}
