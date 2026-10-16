// Package faultvm models demand installation of GPU mappings with a queued host
// translation service. Data placement is fixed; no migration or OS is simulated.
package faultvm

import (
	"github.com/sarchlab/akita/v5/mem/vm"
	"github.com/sarchlab/akita/v5/mem/vm/vmprotocol"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/modeling"
	"github.com/sarchlab/akita/v5/timing"
	"github.com/sarchlab/mgpusim/v5/amd/timing/localmapping"
)

// All components use a 1 GHz clock. Parameters below are cycle counts, not
// calibrated hardware measurements. Akita timestamps are picoseconds.
const Cycle = timing.VTimeInPicoSec(1000)

// Config specifies finite resources and timing for the demand-mapping model.
type Config struct {
	LocalWalkCycles int
	LocalWalkers    int
	MaxTransactions int
	MaxWaiters      int
	HostWalkCycles  int
	HostWalkers     int
	HostQueueSize   int
	LinkCycles      int
	InstallCycles   int
}

// DefaultConfig provides explicit, adjustable starting assumptions.
func DefaultConfig() Config {
	return Config{LocalWalkCycles: 100, LocalWalkers: 8, MaxTransactions: 64,
		MaxWaiters: 64, HostWalkCycles: 400, HostWalkers: 16, HostQueueSize: 64,
		LinkCycles: 200, InstallCycles: 100}
}

// Validate rejects invalid capacities and timing before simulation starts.
func (c Config) Validate() {
	if c.LocalWalkCycles < 1 || c.HostWalkCycles < 1 || c.LocalWalkers < 1 ||
		c.HostWalkers < 1 || c.HostQueueSize < 1 || c.MaxTransactions < c.LocalWalkers ||
		c.MaxWaiters < 1 || c.LinkCycles < 0 || c.InstallCycles < 0 {
		panic("faultvm: invalid capacity or cycle count")
	}
}

// FaultRequest identifies a page, process, and the point at which it was sent.
// There is one request per locally coalesced fault; cross-GPU requests are not
// coalesced at the host in this baseline.
type FaultRequest struct {
	messaging.MsgMeta
	PID    vm.PID
	VAddr  uint64
	SentAt timing.VTimeInPicoSec
}

// FaultResponse returns an existing allocation, never a newly invented page.
type FaultResponse struct {
	messaging.MsgMeta
	Page vm.Page
}

var faultProtocol = messaging.DefineProtocol("mgpusim.mappingfault",
	messaging.RoleDef{Name: "requester", Sends: []messaging.Msg{FaultRequest{}}},
	messaging.RoleDef{Name: "responder", Sends: []messaging.Msg{FaultResponse{}}},
)

// GPUStats distinguishes incoming translation requests from coalesced page walks.
type GPUStats struct {
	Requests, Completed, Coalesced                uint64
	Walks, LocalHits, LocalFaults                 uint64
	FaultSent, FaultResolved                      uint64
	OutstandingHighWater                          uint64
	AdmissionBlockedCycles, HostSendBlockedCycles uint64
	LatencySum, LatencyMax                        timing.VTimeInPicoSec
	FaultLatencySum, FaultLatencyMax              timing.VTimeInPicoSec
	LocalQueueWaitSum                             timing.VTimeInPicoSec
}

// HostStats measures queueing separately from configured service time.
type HostStats struct {
	Received, Completed                    uint64
	QueueHighWater, ActiveHighWater        uint64
	QueueFullCycles, ResponseBlockedCycles uint64
	QueueWaitSum, QueueWaitMax             timing.VTimeInPicoSec
	IngressWaitSum, ServiceSum             timing.VTimeInPicoSec
}

type stage uint8

const (
	waitingWalker stage = iota
	walking
	requestFlight
	waitingHost
	responseFlight
	installing
	ready
)

type waiter struct {
	Req     vmprotocol.TranslationReq
	Arrived timing.VTimeInPicoSec
}

type transaction struct {
	PID                    vm.PID
	VAddr                  uint64
	Waiters                []waiter
	Stage                  stage
	Due                    timing.VTimeInPicoSec
	Enqueued, FaultStarted timing.VTimeInPicoSec
	HostID                 uint64
	Page                   vm.Page
}

// GPUSpec includes the immutable routing destination for host requests.
type GPUSpec struct {
	LocalWalkCycles int
	LocalWalkers    int
	MaxTransactions int
	MaxWaiters      int
	HostWalkCycles  int
	HostWalkers     int
	HostQueueSize   int
	LinkCycles      int
	InstallCycles   int
	Host            messaging.RemotePort
	Log2PageSize    uint64
}

// MakeGPUSpec flattens the configuration as required by Akita's Spec schema.
func MakeGPUSpec(c Config, host messaging.RemotePort, log2PageSize uint64) GPUSpec {
	return GPUSpec{LocalWalkCycles: c.LocalWalkCycles, LocalWalkers: c.LocalWalkers,
		MaxTransactions: c.MaxTransactions, MaxWaiters: c.MaxWaiters,
		HostWalkCycles: c.HostWalkCycles, HostWalkers: c.HostWalkers, HostQueueSize: c.HostQueueSize,
		LinkCycles: c.LinkCycles, InstallCycles: c.InstallCycles, Host: host, Log2PageSize: log2PageSize}
}

// Config reconstructs the common GPU/host configuration from immutable fields.
func (s GPUSpec) Config() Config {
	return Config{LocalWalkCycles: s.LocalWalkCycles, LocalWalkers: s.LocalWalkers,
		MaxTransactions: s.MaxTransactions, MaxWaiters: s.MaxWaiters,
		HostWalkCycles: s.HostWalkCycles, HostWalkers: s.HostWalkers, HostQueueSize: s.HostQueueSize,
		LinkCycles: s.LinkCycles, InstallCycles: s.InstallCycles}
}

// GPUState contains finite translation slots, each with bounded coalesced waiters.
type GPUState struct {
	Transactions []transaction
	Stats        GPUStats
}

// GPUResources wires the per-GPU mapping table.
type GPUResources struct{ Table *localmapping.View }

// GPU is a local GMMU endpoint using the standard Akita translation protocol.
type GPU = modeling.Component[GPUSpec, GPUState, GPUResources]

type hostJob struct {
	Req                    FaultRequest
	Enqueued, Started, Due timing.VTimeInPicoSec
	Page                   vm.Page
	Resolved               bool
}

// HostState separates waiting jobs from occupied translation service slots.
type HostState struct {
	Queue  []hostJob
	Active []hostJob
	Stats  HostStats
}

// HostResources points at the authoritative allocation table.
type HostResources struct{ Table vm.PageTable }

// Host is the shared, finite-capacity host mapping service.
type Host = modeling.Component[Config, HostState, HostResources]
