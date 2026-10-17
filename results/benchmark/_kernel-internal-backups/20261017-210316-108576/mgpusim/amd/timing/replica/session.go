package replica

import (
	"fmt"
	"github.com/sarchlab/akita/v5/mem/vm/addresstranslator"
	"sort"

	"github.com/sarchlab/akita/v5/mem/vm"
	"github.com/sarchlab/akita/v5/mem/vm/lruset"
	"github.com/sarchlab/akita/v5/mem/vm/tlb"
	"github.com/sarchlab/akita/v5/simulation"
	"github.com/sarchlab/akita/v5/timing"
	"github.com/sarchlab/akita/v5/tracing"
	"github.com/sarchlab/mgpusim/v5/amd/driver"
	"github.com/sarchlab/mgpusim/v5/amd/timing/cu"
	"github.com/sarchlab/mgpusim/v5/amd/timing/faultvm"
	"github.com/sarchlab/mgpusim/v5/amd/timing/localmapping"
	"github.com/sarchlab/mgpusim/v5/amd/timing/rdma"
)

type copyState struct {
	Version       uint64 // whole-page version; partial repairs use SectorVersion
	Readable      bool
	SectorVersion map[uint32]uint64
	Frame         uint64
	Valid         map[uint32]bool
	Had           bool
	Stamp         uint64
}
type pageState struct {
	Version   uint64
	LastWrite map[uint32]uint64
	Home      vm.Page
	Retired   bool
	Copies    map[int]*copyState
}

// Stats separates observed execution time from modeled oracle costs. Counts at
// the translator are transactions, never claimed to be DRAM transactions.
type RecoveryEvent struct {
	HomeGPU      int                    `json:"home_gpu"`
	Role         string                 `json:"page_role"`
	RepairKind   string                 `json:"repair_kind"`
	Epoch        int                    `json:"epoch"`
	Page         uint64                 `json:"page"`
	GPU          int                    `json:"gpu"`
	Version      uint64                 `json:"source_version"`
	Cold         bool                   `json:"cold"`
	Bytes        uint64                 `json:"functional_bytes"`
	ChargedBytes uint64                 `json:"charged_bytes"`
	StartPS      uint64                 `json:"start_ps"`
	DuePS        uint64                 `json:"due_ps"`
	Layouts      map[uint64]Granularity `json:"layouts"`
}

type Stats struct {
	InitialInputSHA256          string                 `json:"initial_input_sha256,omitempty"`
	Passive                     *PassiveStats          `json:"passive,omitempty"`
	Adapter                     string                 `json:"adapter,omitempty"`
	CoreAllocations             []ManagedAllocation    `json:"core_allocations,omitempty"`
	TerminalSHA256              string                 `json:"terminal_sha256,omitempty"`
	Ownership                   []PageOwnership        `json:"page_ownership"`
	HomePages                   map[int]uint64         `json:"home_pages_by_gpu"`
	OwnerReadTransactions       uint64                 `json:"owner_read_transactions"`
	NonOwnerReadTransactions    uint64                 `json:"non_owner_read_transactions"`
	OwnerWriteTransactions      uint64                 `json:"owner_write_transactions"`
	NonOwnerWriteTransactions   uint64                 `json:"non_owner_write_transactions"`
	ReplicaReadTransactions     uint64                 `json:"replica_read_transactions"`
	RemoteHomeReadTransactions  uint64                 `json:"remote_home_read_transactions"`
	CopyBytesByRole             map[string]uint64      `json:"copy_bytes_by_role"`
	CopyFaultsByRole            map[string]uint64      `json:"copy_faults_by_role"`
	RecoveryWaitByRole          map[string]uint64      `json:"recovery_wait_ps_by_role"`
	FabricModel                 string                 `json:"fabric_model"`
	ManagedPages                uint64                 `json:"managed_pages"`
	Activity                    []EpochActivity        `json:"epoch_activity"`
	ModelRevision               string                 `json:"model_revision"`
	OracleMappingPreinstall     bool                   `json:"oracle_mapping_preinstall"`
	MappingInstalls             uint64                 `json:"mapping_installs"`
	TLBFlushEpochs              uint64                 `json:"tlb_flush_epochs"`
	MappingFaults               uint64                 `json:"mapping_faults"`
	FallbackPageEpochs          uint64                 `json:"fallback_page_epochs"`
	MultiWriterPageEpochs       uint64                 `json:"multiple_writer_page_epochs"`
	ReadWriteConflictPageEpochs uint64                 `json:"read_write_overlap_page_epochs"`
	FallbackGPUPageAccesses     uint64                 `json:"fallback_gpu_page_accesses"`
	FallbackReadTransactions    uint64                 `json:"fallback_read_transactions"`
	FallbackWriteTransactions   uint64                 `json:"fallback_write_transactions"`
	FallbackEvents              []FallbackEvent        `json:"fallback_events"`
	WriteEpochNewCopies         uint64                 `json:"write_epoch_new_copies"`
	WriteEpochNewCopyBytes      uint64                 `json:"write_epoch_new_copy_bytes"`
	WriteEpochNewCopyFaults     uint64                 `json:"write_epoch_new_copy_faults"`
	WriteEpochExistingReads     uint64                 `json:"write_epoch_existing_replica_reads"`
	WriteEpochNewReads          uint64                 `json:"write_epoch_new_replica_reads"`
	RecoveryEvents              []RecoveryEvent        `json:"recovery_events"`
	S4Targets                   uint64                 `json:"s4_inv_replica_targets"`
	WritePageEpochs             uint64                 `json:"write_page_epochs"`
	VersionedPages              uint64                 `json:"versioned_pages"`
	Config                      Config                 `json:"config"`
	Epochs                      uint64                 `json:"epochs"`
	KernelPS                    uint64                 `json:"kernel_ps"`
	HostTransferPS              uint64                 `json:"host_transfer_ps"`
	ValidationSHA256            string                 `json:"validation_sha256,omitempty"`
	KernelCount                 uint64                 `json:"kernel_count"`
	HostWriteEpochs             uint64                 `json:"host_write_epochs"`
	PeakLiveManagedPages        uint64                 `json:"peak_live_managed_pages"`
	RetiredManagedPages         uint64                 `json:"retired_managed_pages"`
	ApplicationPS               uint64                 `json:"application_ps"`
	BarrierFlushPS              uint64                 `json:"barrier_flush_ps"`
	RecoveryWaitPS              uint64                 `json:"recovery_wait_ps"`
	Faults                      uint64                 `json:"copy_faults"`
	ColdCopies                  uint64                 `json:"cold_copies"`
	Refills                     uint64                 `json:"refills"`
	Repairs                     uint64                 `json:"repairs"`
	CopyBytes                   uint64                 `json:"copy_bytes"`
	Invalidations               uint64                 `json:"invalidations"`
	Evictions                   uint64                 `json:"evictions"`
	PeakReplicaPages            uint64                 `json:"peak_replica_pages"`
	PeakStalePages              uint64                 `json:"peak_stale_pages"`
	RetainedPages               uint64                 `json:"retained_pages"`
	S2CleanTransactions         uint64                 `json:"s2_clean_transactions"`
	S3EligibleTransactions      uint64                 `json:"s3_preexisting_replica_read_transactions"`
	S3EligibleSectors           uint64                 `json:"s3_preexisting_replica_read_sectors"`
	S3ConflictSectors           uint64                 `json:"s3_conflicting_read_sectors"`
	Granularities               map[uint64]Granularity `json:"repair_granularities"`
	FabricBytes                 uint64                 `json:"fabric_bytes"`
	FabricMessages              uint64                 `json:"fabric_messages"`
}

// Session is explicitly driven at GLOBAL drained kernel boundaries. It is
// deliberately separate from -vm-mode=demand, which still models mapping faults.
type Session struct {
	Passive           *Passive
	observedReads     map[[2]uint64]map[uint32]bool
	observedWrites    map[[2]uint64]map[uint32]bool
	plannedAccess     map[[2]uint64]*Access
	plannedReads      map[[2]uint64]map[uint32]bool
	plannedWrites     map[[2]uint64]map[uint32]bool
	Dynamic           bool
	TerminalDigest    bool
	epochActivity     *EpochActivity
	activityCounts    map[int]int
	activityLast      uint64
	uses              map[uint64]*PageUse
	existedAtStart    map[[2]uint64]bool
	mappingFaultStart uint64
	shadowStamp       map[[2]uint64]uint64
	shadowClock       uint64
	Config            Config
	Plan              *Plan
	Recorded          *Plan
	Stats             Stats
	Fabric            *Fabric
	d                 *driver.Driver
	sim               *simulation.Simulation
	ctx               *driver.Context
	tables            *localmapping.Tables
	pages             map[uint64]*pageState
	active            *Epoch
	observed          map[[2]uint64]*Access
	mapped            map[[2]uint64]bool
	baselineValid     map[[2]uint64]bool
	shadow            map[[2]uint64]bool
	at                int
	clock             uint64
	started           timing.VTimeInPicoSec
	lastEnd           timing.VTimeInPicoSec
	phaseKind         string
}

// New attaches request tracers and a shared fabric, before any GPU execution.
// A nil plan records pass one with demand translation. All replay policies,
// including norep, share free mapping preparation and identical TLB clearing.
func New(sim *simulation.Simulation, d *driver.Driver, ctx *driver.Context, c Config, p *Plan) *Session {
	c.Validate()
	if uint64(1)<<d.Log2PageSize != c.PageBytes {
		panic("replica: hardware and plan page sizes differ")
	}
	tables, ok := d.Resources().PageTable.(*localmapping.Tables)
	if !ok {
		panic("replica: demand page tables required")
	}
	if p != nil {
		p.Validate()
		if p.PageBytes != c.PageBytes || p.SectorBytes != c.SectorBytes {
			panic("replica: plan/config geometry mismatch")
		}
	}
	s := &Session{Config: c, Plan: p, d: d, sim: sim, ctx: ctx, tables: tables, pages: map[uint64]*pageState{},
		Fabric: NewFabric(c), Recorded: &Plan{Format: 1, PageBytes: c.PageBytes, SectorBytes: c.SectorBytes},
		Stats: Stats{ModelRevision: "replica-owner-v3", FabricModel: "destination-ready-v2",
			OracleMappingPreinstall: p != nil,
			Config:                  c, Granularities: map[uint64]Granularity{}}, shadow: map[[2]uint64]bool{},
		shadowStamp: map[[2]uint64]uint64{}}
	for _, comp := range sim.Components() {
		if _, ok := comp.(*cu.Comp); ok {
			var gpu int
			if _, err := fmt.Sscanf(comp.Name(), "GPU[%d]", &gpu); err == nil {
				tracing.CollectTrace(comp.(tracing.NamedHookable), &activityTracer{session: s, gpu: gpu, insts: map[uint64]bool{}})
			}
		}
		if v, ok := comp.(*rdma.Comp); ok {
			rdma.SetFabric(v, s.Fabric)
		}
		if _, ok := comp.(*addresstranslator.Comp); ok {
			var gpu int
			if _, err := fmt.Sscanf(comp.Name(), "GPU[%d]", &gpu); err == nil {
				tracing.CollectTrace(comp.(tracing.NamedHookable), &accessTracer{session: s, gpu: gpu})
			}
		}
	}
	s.mappingFaultStart = s.mappingFaults()
	return s
}

// Manage registers a page-aligned unified allocation after host initialization.
// Host writes to these buffers after registration require a new explicit phase
// and are not supported implicitly.
func (s *Session) Manage(ptr driver.Ptr, bytes uint64) {
	if uint64(ptr)%s.Config.PageBytes != 0 || bytes == 0 {
		panic("replica: managed region must start at a page boundary")
	}
	for a := uint64(ptr); a < uint64(ptr)+bytes; a += s.Config.PageBytes {
		p, ok := s.tables.Find(s.ctx.PID(), a)
		if !ok || !p.Unified {
			panic("replica: managed allocation is not unified")
		}
		s.pages[a] = &pageState{Home: p, Copies: map[int]*copyState{}, LastWrite: map[uint32]uint64{}}
		if s.Passive != nil {
			seen := map[uint64]bool{}
			for _, at := range s.Passive.ats {
				gpu := at.Spec().DeviceID
				if !seen[gpu] {
					s.tables.View(int(gpu) - 1).InstallAtBarrier(p)
					s.Stats.MappingInstalls++
					seen[gpu] = true
				}
			}
		}
	}
}

// Epoch runs one global phase. work must submit all its kernels and drain all
// queues before returning. Every oracle mode pays identical physical cache
// writeback/invalidation at the barrier; PTE and TLB changes themselves are free.
func (s *Session) Epoch(name string, work func()) {
	s.d.WaitForEngineIdle()
	if s.at == 0 {
		s.started = s.sim.GetEngine().CurrentTime()
	} else if s.Dynamic {
		s.Stats.HostTransferPS += uint64(s.sim.GetEngine().CurrentTime() - s.lastEnd)
	}
	s.active = &Epoch{Name: name}
	s.observed = map[[2]uint64]*Access{}
	s.observedReads = map[[2]uint64]map[uint32]bool{}
	s.observedWrites = map[[2]uint64]map[uint32]bool{}
	s.mapped = map[[2]uint64]bool{}
	s.baselineValid = map[[2]uint64]bool{}
	s.rememberExisting()
	if s.Plan != nil {
		if s.at >= len(s.Plan.Epochs) || s.Plan.Epochs[s.at].Name != name {
			panic("replica: workload/plan epoch mismatch")
		}
		s.active = &s.Plan.Epochs[s.at]
		s.plannedAccess = map[[2]uint64]*Access{}
		s.plannedReads = map[[2]uint64]map[uint32]bool{}
		s.plannedWrites = map[[2]uint64]map[uint32]bool{}
		for i := range s.active.Accesses {
			a := &s.active.Accesses[i]
			k := [2]uint64{a.Page, uint64(a.GPU)}
			s.plannedAccess[k] = a
			s.plannedReads[k] = set(a.Reads)
			s.plannedWrites[k] = set(a.Writes)
		}
		s.prepare()
	}
	start := s.sim.GetEngine().CurrentTime()
	s.beginActivity(name, uint64(start))
	work()
	s.d.WaitForEngineIdle()
	kernel := s.sim.GetEngine().CurrentTime() - start
	if s.phaseKind != "" {
		s.Stats.HostTransferPS += uint64(kernel)
		if s.phaseKind == "host-write" {
			s.Stats.HostWriteEpochs++
		}
	} else {
		s.Stats.KernelPS += uint64(kernel)
		if s.Dynamic {
			s.Stats.KernelCount++
		}
	}
	s.endActivity(uint64(start + kernel))
	// Capture all actual virtual read/write requests before ending the epoch.
	out := Epoch{Name: name, KernelPS: uint64(kernel)}
	if s.Dynamic && s.phaseKind != "" {
		out.Kind = s.phaseKind
	} else if s.Dynamic {
		out.Kind = "kernel"
	}
	for k, a := range s.observed {
		a.Reads = sorted(s.observedReads[k])
		a.Writes = sorted(s.observedWrites[k])
		out.Accesses = append(out.Accesses, *a)
	}
	sort.Slice(out.Accesses, func(i, j int) bool {
		a, b := out.Accesses[i], out.Accesses[j]
		if a.Page != b.Page {
			return a.Page < b.Page
		}
		return a.GPU < b.GPU
	})
	s.Recorded.Epochs = append(s.Recorded.Epochs, out)
	s.Recorded.Validate()
	if s.Plan != nil {
		s.checkObserved(out)
	}
	s.active = nil
	before := s.sim.GetEngine().CurrentTime()
	s.d.FlushAtBarrier(s.ctx)
	if s.Passive != nil {
		s.Passive.CheckAtBarrier()
	}
	s.Stats.BarrierFlushPS += uint64(s.sim.GetEngine().CurrentTime() - before)
	if s.Plan != nil {
		s.commit(s.Plan.Epochs[s.at])
	}
	s.at++
	s.Stats.Epochs++
	s.lastEnd = s.sim.GetEngine().CurrentTime()
	s.Stats.ApplicationPS = uint64(s.lastEnd - s.started)
}

func (s *Session) invalidateTLBs() {
	for _, c := range s.sim.Components() {
		v, ok := c.(*tlb.Comp)
		if !ok {
			continue
		}
		if len(v.State.MSHREntries) != 0 || v.State.HasRespondingMSHR ||
			len(v.State.Pipeline.Stages()) != 0 || v.State.BufferItems.Size() != 0 {
			panic("replica: TLB is not quiescent")
		}
		for i := range v.State.Sets {
			st := &v.State.Sets[i]
			st.LRU = lruset.NewSet(len(st.Blocks))
			for j := range st.Blocks {
				st.Blocks[j].Page = vm.Page{}
			}
		}
	}
}

// Finish saves independently observed replay accesses, counts, and elapsed time.
func (s *Session) Finish(planPath, statsPath string) {
	if s.Passive != nil {
		s.Passive.Finish()
	}
	if s.Dynamic && s.at > 0 {
		now := s.sim.GetEngine().CurrentTime()
		s.Stats.HostTransferPS += uint64(now - s.lastEnd)
		s.Stats.ApplicationPS = uint64(now - s.started)
		s.lastEnd = now
	}
	if s.Plan != nil && s.at != len(s.Plan.Epochs) {
		panic("replica: plan was not fully consumed")
	}
	total := 0
	for _, e := range s.Recorded.Epochs {
		total += len(e.Accesses)
	}
	if total == 0 {
		panic("replica: no managed GPU accesses were captured")
	}
	s.finishOwnership()
	s.Stats.ManagedPages = uint64(len(s.pages))
	s.Stats.MappingFaults = s.mappingFaults() - s.mappingFaultStart
	s.Stats.FabricBytes = s.Fabric.Bytes
	s.Stats.FabricMessages = s.Fabric.Messages
	s.snapshot()
	if planPath != "" {
		s.Recorded.Save(planPath)
	}
	if statsPath != "" {
		writeJSON(statsPath, s.Stats)
	}
}

func (s *Session) checkObserved(out Epoch) {
	if s.Plan.Conservative {
		return
	} // Every actual access is still checked by guard against the declared superset.
	if len(out.Accesses) != len(s.active.Accesses) {
		panic("replica: replay changed accessed page/GPU set")
	}
	for _, a := range out.Accesses {
		p := s.planned(a.Page, a.GPU)
		if fmt.Sprint(a.Reads) != fmt.Sprint(p.Reads) || fmt.Sprint(a.Writes) != fmt.Sprint(p.Writes) {
			panic("replica: replay changed read/write set")
		}
	}
}
func (s *Session) planned(page uint64, gpu int) *Access {
	if a := s.plannedAccess[[2]uint64{page, uint64(gpu)}]; a != nil {
		return a
	}
	for i := range s.active.Accesses {
		a := &s.active.Accesses[i]
		if a.Page == page && a.GPU == gpu {
			return a
		}
	}
	panic(fmt.Sprintf("replica: unplanned page %#x on GPU %d", page, gpu))
}

func (s *Session) mappingFaults() uint64 {
	var n uint64
	for _, c := range s.sim.Components() {
		if m, ok := c.(*faultvm.GPU); ok {
			n += m.State.Stats.LocalFaults
		}
	}
	return n
}

func (s *Session) rememberExisting() {
	s.existedAtStart = map[[2]uint64]bool{}
	for page, st := range s.pages {
		for gpu, c := range st.Copies {
			if c.Frame != 0 && c.Had {
				s.existedAtStart[[2]uint64{page, uint64(gpu)}] = true
			}
		}
	}
}
