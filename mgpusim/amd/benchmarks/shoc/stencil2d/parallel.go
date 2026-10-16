package stencil2d

import (
	"fmt"
	"log"
	"math"

	"github.com/sarchlab/mgpusim/v5/amd/driver"
	"github.com/sarchlab/mgpusim/v5/amd/insts"
)

// ParallelBenchmark performs a real double-buffered nine-point Jacobi stencil.
// Each GPU owns contiguous interior rows and reads one neighboring halo row.
// All GPU kernels are enqueued before any queue is drained at each iteration.
// It reuses the repository's GCN3 SHOC kernel without changing its arithmetic.
type ParallelBenchmark struct {
	HomePlacement          string
	AlignOwnerPages        bool
	gridBases              []driver.Ptr
	gridSizes              []uint64
	gridPrefix             uint64
	OverlapHalo            bool
	snapshots              []driver.Ptr
	haloQueues             []*driver.CommandQueue
	MemoryReady            func(*driver.Context, []driver.Ptr, []uint64)
	Epoch                  func(string, func())
	Rows, Cols, Iterations int
	d                      *driver.Driver
	ctx                    *driver.Context
	gpus                   []int
	contexts               []*driver.Context
	queues                 []*driver.CommandQueue
	kernels                []*insts.KernelCodeObject
	pitch                  int
	src, dst               driver.Ptr
	initial                []float32
}

// NewParallelBenchmark constructs an independently partitioned GCN3 workload.
func NewParallelBenchmark(d *driver.Driver) *ParallelBenchmark {
	return &ParallelBenchmark{d: d, ctx: d.Init()}
}

// SelectGPU selects the independent GPUs, in row-partition order.
func (b *ParallelBenchmark) SelectGPU(ids []int) { b.gpus = ids }

// SetUnifiedMemory is required by this explicit replication experiment.
func (*ParallelBenchmark) SetUnifiedMemory() {}

// Run launches one concurrent kernel per GPU at every global iteration.
func (b *ParallelBenchmark) Run() {
	b.validate()
	b.initialize()
	if b.OverlapHalo {
		b.runOverlap()
		return
	}
	for step := 0; step < b.Iterations; step++ {
		work := func() { b.launchPartitions() }
		if b.Epoch != nil {
			b.Epoch(fmt.Sprintf("parallel-jacobi-%d", step), work)
		} else {
			work()
		}
		b.src, b.dst = b.dst, b.src
	}
}
func (b *ParallelBenchmark) validate() {
	if len(b.gpus) < 2 || b.Rows < 2+16*len(b.gpus) || (b.Rows-2)%(16*len(b.gpus)) != 0 ||
		b.Cols < 66 || (b.Cols-2)%64 != 0 || b.Iterations < 1 ||
		(b.OverlapHalo && (b.Rows-2)/len(b.gpus) < 48) {
		panic("parallel stencil: interior rows must divide 16*numGPU; interior columns must divide 64")
	}
}
func (b *ParallelBenchmark) initialize() {
	b.pitch = (b.Cols + 15) / 16 * 16
	b.initial = make([]float32, b.Rows*b.pitch)
	for row := 0; row < b.Rows; row++ {
		for col := 0; col < b.Cols; col++ {
			b.initial[row*b.pitch+col] = float32((row*17+col*13)%97+1) / 97
		}
	}
	b.d.SelectGPU(b.ctx, b.gpus[0])
	bytes := uint64(len(b.initial)) * 4
	pageBytes := uint64(1) << b.d.Log2PageSize
	if b.AlignOwnerPages {
		ownedBytes := uint64((b.Rows - 2) / len(b.gpus) * b.pitch * 4)
		if ownedBytes%pageBytes != 0 {
			panic("owner-aligned stencil: partition bytes must be a page multiple; use 64 interior rows/GPU for 4KiB")
		}
		b.gridPrefix = (pageBytes - uint64(b.pitch*4)%pageBytes) % pageBytes
	}
	b.src = b.allocateGrid(bytes)
	if !b.OverlapHalo {
		b.dst = b.allocateGrid(bytes)
	}
	b.d.MemCopyH2D(b.ctx, b.src, b.initial)
	if !b.OverlapHalo {
		b.d.MemCopyH2D(b.ctx, b.dst, b.initial)
	}
	for _, gpu := range b.gpus {
		ctx := b.d.InitWithExistingPID(b.ctx)
		b.d.SelectGPU(ctx, gpu)
		b.contexts = append(b.contexts, ctx)
		b.queues = append(b.queues, b.d.CreateCommandQueue(ctx))
		// Distinct code objects ensure each GPU's instruction storage is local.
		co := insts.LoadKernelCodeObjectFromBytes(gcn3HSACOBytes, "StencilKernel")
		if co == nil {
			panic("parallel stencil kernel unavailable")
		}
		b.kernels = append(b.kernels, co)
		if b.OverlapHalo {
			rows := (b.Rows - 2) / len(b.gpus)
			b.snapshots = append(b.snapshots, b.d.AllocateMemory(ctx, uint64((rows+2)*b.pitch*4)))
			b.haloQueues = append(b.haloQueues, b.d.CreateCommandQueue(ctx))
		}
	}
	if b.MemoryReady != nil {
		b.MemoryReady(b.ctx, b.gridBases, b.gridSizes)
	}
}
func (b *ParallelBenchmark) launchPartitions() {
	rows := (b.Rows - 2) / len(b.gpus)
	b.d.EnqueueAtBarrier(func() {
		for i, q := range b.queues {
			offset := driver.Ptr(i * rows * b.pitch * 4)
			args := StencilKernelArgs{Data: b.src + offset, NewData: b.dst + offset, Alignment: 16,
				WCenter: 0.5, WCardinal: 0.1, WDiagonal: 0.025, Sh: driver.LocalPtr(18 * 66 * 4)}
			b.d.EnqueueLaunchKernel(q, b.kernels[i], [3]uint32{uint32(rows / 16), uint32(b.Cols - 2), 1},
				[3]uint16{1, 64, 1}, &args)
		}
	})
	for _, q := range b.queues {
		b.d.DrainCommandQueue(q)
	}
}

// Verify compares every interior and boundary value with CPU Jacobi steps.
func (b *ParallelBenchmark) Verify() {
	a := append([]float32(nil), b.initial...)
	out := append([]float32(nil), b.initial...)
	for step := 0; step < b.Iterations; step++ {
		b.cpuStep(a, out)
		a, out = out, a
	}
	got := make([]float32, len(a))
	b.d.MemCopyD2H(b.ctx, got, b.src)
	for row := 0; row < b.Rows; row++ {
		for col := 0; col < b.Cols; col++ {
			i := row*b.pitch + col
			if math.IsNaN(float64(got[i])) || math.Abs(float64(got[i]-a[i])) > 1e-5 {
				panic(fmt.Sprintf("parallel stencil mismatch (%d,%d): got %g want %g", row, col, got[i], a[i]))
			}
		}
	}
	log.Printf("Passed! parallel Jacobi %dx%d, %d GPUs, %d iterations, halo overlap=%t",
		b.Rows, b.Cols, len(b.gpus), b.Iterations, b.OverlapHalo)
}
func (b *ParallelBenchmark) cpuStep(a, out []float32) {
	for row := 1; row < b.Rows-1; row++ {
		for col := 1; col < b.Cols-1; col++ {
			i := row*b.pitch + col
			cardinal := a[i-b.pitch] + a[i+b.pitch] + a[i+1] + a[i-1]
			diagonal := a[i-b.pitch+1] + a[i+b.pitch+1] + a[i-b.pitch-1] + a[i+b.pitch-1]
			out[i] = 0.5*a[i] + 0.1*cardinal + 0.025*diagonal
		}
	}
}

// runOverlap is a conventional interior/halo/boundary Jacobi schedule. Global
// writes use one grid; private old-value snapshots make the update race-free.
func (b *ParallelBenchmark) runOverlap() {
	for step := 0; step < b.Iterations; step++ {
		b.runPhase(fmt.Sprintf("jacobi-snapshot-%d", step), b.snapshotInterior)
		b.runPhase(fmt.Sprintf("jacobi-bulk-halo-%d", step), b.bulkAndHalo)
		b.runPhase(fmt.Sprintf("jacobi-boundary-%d", step), b.boundaries)
	}
}
func (b *ParallelBenchmark) runPhase(name string, work func()) {
	if b.Epoch != nil {
		b.Epoch(name, work)
	} else {
		work()
	}
}
func (b *ParallelBenchmark) snapshotInterior() {
	rows := (b.Rows - 2) / len(b.gpus)
	pitchBytes := b.pitch * 4
	b.d.EnqueueAtBarrier(func() {
		for i, q := range b.queues {
			b.d.EnqueueMemCopyD2D(q, b.snapshots[i]+driver.Ptr(pitchBytes),
				b.src+driver.Ptr((i*rows+1)*pitchBytes), rows*pitchBytes)
		}
	})
	b.drain(b.queues)
}
func (b *ParallelBenchmark) bulkAndHalo() {
	rows := (b.Rows - 2) / len(b.gpus)
	pitchBytes := b.pitch * 4
	b.d.EnqueueAtBarrier(func() {
		for i, q := range b.queues {
			// Interior tiles never read the not-yet-fetched halo rows.
			b.enqueueTile(q, i, 16, rows-32)
			halo := b.haloQueues[i]
			b.d.EnqueueMemCopyD2D(halo, b.snapshots[i], b.src+driver.Ptr(i*rows*pitchBytes), pitchBytes)
			b.d.EnqueueMemCopyD2D(halo, b.snapshots[i]+driver.Ptr((rows+1)*pitchBytes),
				b.src+driver.Ptr((i*rows+rows+1)*pitchBytes), pitchBytes)
		}
	})
	b.drain(b.queues)
	b.drain(b.haloQueues)
}
func (b *ParallelBenchmark) boundaries() {
	rows := (b.Rows - 2) / len(b.gpus)
	b.d.EnqueueAtBarrier(func() {
		for i, q := range b.queues {
			b.enqueueTile(q, i, 0, 16)
			b.enqueueTile(q, i, rows-16, 16)
		}
	})
	b.drain(b.queues)
}
func (b *ParallelBenchmark) enqueueTile(q *driver.CommandQueue, gpu, offset, rows int) {
	owned := (b.Rows - 2) / len(b.gpus)
	pitchBytes := b.pitch * 4
	args := StencilKernelArgs{Data: b.snapshots[gpu] + driver.Ptr(offset*pitchBytes),
		NewData: b.src + driver.Ptr((gpu*owned+offset)*pitchBytes), Alignment: 16,
		WCenter: 0.5, WCardinal: 0.1, WDiagonal: 0.025, Sh: driver.LocalPtr(18 * 66 * 4)}
	b.d.EnqueueLaunchKernel(q, b.kernels[gpu], [3]uint32{uint32(rows / 16), uint32(b.Cols - 2), 1},
		[3]uint16{1, 64, 1}, &args)
}
func (b *ParallelBenchmark) drain(qs []*driver.CommandQueue) {
	for _, q := range qs {
		b.d.DrainCommandQueue(q)
	}
}
