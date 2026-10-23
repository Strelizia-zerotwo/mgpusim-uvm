package runner

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/sarchlab/mgpusim/v5/amd/driver"
	"github.com/sarchlab/mgpusim/v5/amd/timing/replica"
	"os"
	"time"
)

var replicaCoreFlag = flag.Bool("replica-core", false, "Core benchmark replication experiment")
var coreSystemSync = flag.Bool("core-has-system-scope-sync", false, "Conservative home reads for written pages in manually annotated system-scope kernels; does not emulate atomics")
var coreProtocol = flag.String("core-protocol", "ideal", "Replication protocol: ideal (free coherence inside kernels) or kernel-barrier (historical experiment)")
var coreConfig = flag.String("core-replica-config", "", "Core replica config JSON")
var corePlan = flag.String("core-replica-plan", "", "Core replica oracle plan (omit to record)")
var corePlanOut = flag.String("core-replica-plan-out", "observed-plan.json", "Actual core accesses")
var coreStats = flag.String("core-replica-stats", "stats.json", "Core replica statistics")
var coreTerminalDigest = flag.Bool("core-digest-terminal", false, "Hash live logical buffers at kernel boundaries (BFS scheduling validation)")
var coreProgressSeconds = flag.Int("core-progress-seconds", 30, "Host seconds between progress samples; 0 disables diagnostics")
var coreProgressFile = flag.String("core-progress-file", "progress.jsonl", "Live progress and native idle-engine stack diagnostics")
var coreSessions = map[*Runner]*replica.Session{}

func (r *Runner) initCoreReplica() {
	if !*replicaCoreFlag {
		return
	}
	if !r.Timing || r.Parallel || *vmModeFlag != "demand" || *unifiedGPUFlag == "" {
		panic("replica-core requires serial timing, demand MMUs and explicit unified-gpus dispatch")
	}
	if len(r.benchmarks) > 0 {
		panic("attach core adapter before adding benchmark")
	}
	c := replica.DefaultConfig()
	if *coreConfig != "" {
		b, e := os.ReadFile(*coreConfig)
		if e != nil {
			panic(e)
		}
		if e = json.Unmarshal(b, &c); e != nil {
			panic(e)
		}
	}
	if *coreSystemSync {
		c.HasSystemScopeSync = true
	}
	var p *replica.Plan
	if *coreProtocol != "ideal" && *coreProtocol != "kernel-barrier" {
		panic("unknown core-protocol")
	}
	if *coreProtocol == "ideal" && (*corePlan != "" || c.HasSystemScopeSync) {
		panic("ideal experiment rejects future access plans and hardware system-scope annotations")
	}
	if *coreProtocol == "kernel-barrier" && (c.Policy == "delta" || c.Policy == "delta-sub") {
		panic("delta policies require the ideal in-kernel experiment")
	}
	if *corePlan != "" {
		p = replica.Load(*corePlan)
	}
	ids := r.gpuIDStringToList(*unifiedGPUFlag)
	// First data allocation supplies the benchmark's address-space context.
	r.Driver().ReplicaHooks = &driver.ReplicaHooks{Allocate: func(ctx *driver.Context, bytes uint64) driver.Ptr {
		s := replica.New(r.Simulation(), r.Driver(), ctx, c, p)
		if *coreProtocol == "ideal" {
			replica.EnablePassive(s)
			if *coreProgressSeconds < 0 {
				panic("core-progress-seconds must be nonnegative")
			}
			if err := s.Passive.EnableProgress(*coreProgressFile, time.Duration(*coreProgressSeconds)*time.Second); err != nil {
				panic(err)
			}
		}
		s.TerminalDigest = *coreTerminalDigest
		coreSessions[r] = s
		replica.AttachCore(s, ids)
		return r.Driver().ReplicaHooks.Allocate(ctx, bytes)
	}}
}
func (r *Runner) finishCoreReplica() {
	if !*replicaCoreFlag {
		return
	}
	s := coreSessions[r]
	if s == nil {
		panic("core benchmark allocated no managed data")
	}
	s.Finish(*corePlanOut, *coreStats)
	r.Driver().ReplicaHooks = nil
	fmt.Println("Passed! core replica execution and requested verification completed")
	delete(coreSessions, r)
}

func CoreReplicaEnabled() bool { return *replicaCoreFlag }

func (r *Runner) CoreReplicaEnabled() bool { return *replicaCoreFlag }
