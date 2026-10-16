package runner

import (
	"flag"

	"github.com/sarchlab/mgpusim/v5/amd/timing/faultvm"
)

var vmModeFlag = flag.String("vm-mode", "shared",
	"Translation model: shared (legacy) or demand (GPU-local mapping faults with host service).")
var vmLocalCycles = flag.Int("vm-local-walk-cycles", 100, "Local page-walk service cycles at 1 GHz.")
var vmLocalWalkers = flag.Int("vm-local-walkers", 8, "Concurrent walkers per GPU in demand mode.")
var vmSlots = flag.Int("vm-transaction-slots", 64, "Outstanding page translations per GPU in demand mode.")
var vmWaiters = flag.Int("vm-max-waiters", 64, "Maximum coalesced requesters per outstanding GPU page.")
var vmHostCycles = flag.Int("vm-host-walk-cycles", 400, "Host mapping service cycles at 1 GHz; modeling assumption.")
var vmHostWalkers = flag.Int("vm-host-walkers", 16, "Concurrent host translation service slots.")
var vmHostQueue = flag.Int("vm-host-queue", 64, "Host waiting queue entries, excluding active service slots.")
var vmLinkCycles = flag.Int("vm-link-cycles", 200, "Abstract one-way fault-message delay at 1 GHz.")
var vmInstallCycles = flag.Int("vm-install-cycles", 100, "GPU mapping installation delay at 1 GHz.")

func faultConfigFromFlags() faultvm.Config {
	return faultvm.Config{LocalWalkCycles: *vmLocalCycles, LocalWalkers: *vmLocalWalkers,
		MaxTransactions: *vmSlots, MaxWaiters: *vmWaiters,
		HostWalkCycles: *vmHostCycles, HostWalkers: *vmHostWalkers, HostQueueSize: *vmHostQueue,
		LinkCycles: *vmLinkCycles, InstallCycles: *vmInstallCycles}
}

func (r *Runner) validateFaultFlags() {
	switch *vmModeFlag {
	case "shared":
		flag.Visit(func(f *flag.Flag) {
			if len(f.Name) > 3 && f.Name[:3] == "vm-" && f.Name != "vm-mode" {
				panic("VM timing parameters require -vm-mode=demand")
			}
		})
	case "demand":
		if !r.Timing {
			panic("-vm-mode=demand requires -timing")
		}
		if *unifiedGPUFlag != "" {
			panic("faultvm models independent GPUs; use -gpus instead of -unified-gpus")
		}
		faultConfigFromFlags().Validate()
	default:
		panic("unknown -vm-mode; expected shared or demand")
	}
}
