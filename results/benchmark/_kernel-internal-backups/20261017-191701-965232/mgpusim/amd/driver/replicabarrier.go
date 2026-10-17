package driver

import (
	"fmt"
	"github.com/sarchlab/akita/v5/mem/vm"
	"github.com/sarchlab/akita/v5/timing"
)

// PID returns the context address-space identity to explicit oracle experiments.
func (c *Context) PID() vm.PID { return c.pid }

// FlushAtBarrier writes back and invalidates all GPU caches. The caller must
// have drained EVERY command queue. This is common, charged work in every
// replication comparison, not a zero-cost simulated cache flush.
func (d *Driver) FlushAtBarrier(ctx *Context) {
	d.WaitForEngineIdle()
	q := d.CreateCommandQueue(ctx)
	d.Enqueue(q, &FlushCommand{ID: timing.GetIDGenerator().Generate()})
	d.DrainCommandQueue(q)
	d.WaitForEngineIdle()
}

// AllocateReplicaFrame reserves a real physical frame without publishing a new
// virtual allocation. Call only at a global, drained barrier.
func (d *Driver) AllocateReplicaFrame(gpu int) uint64 {
	if gpu < 1 || gpu >= len(d.devices) {
		panic("replica: invalid GPU")
	}
	return d.devices[gpu].AllocateReplicaFrame()
}

// ReleaseReplicaFrame returns an invalidated frame to its original allocator.
func (d *Driver) ReleaseReplicaFrame(gpu int, addr uint64) { d.devices[gpu].ReleaseReplicaFrame(addr) }

// AdvanceAtBarrier advances simulation time, not wall-clock time. It must only
// be called with all queues drained and no concurrent host submissions.
func (d *Driver) AdvanceAtBarrier(delta timing.VTimeInPicoSec) {
	d.WaitForEngineIdle()
	if delta == 0 {
		return
	}
	d.engineMutex.Lock()
	defer d.engineMutex.Unlock()
	name := fmt.Sprintf("ReplicaBarrierDelay%d", timing.GetIDGenerator().Generate())
	d.engine.(timing.HandlerRegistrar).RegisterHandler(name, barrierDelay{})
	d.engine.Schedule(timing.MakeEventBase(d.engine.CurrentTime()+delta, name))
	if err := d.engine.Run(); err != nil {
		panic(err)
	}
}

type barrierDelay struct{}

func (barrierDelay) Handle(timing.Event) error { return nil }

// EnqueueAtBarrier submits an entire multi-GPU phase before any background
// driver wake can execute it. The callback must enqueue only, never drain.
func (d *Driver) EnqueueAtBarrier(enqueue func()) {
	d.WaitForEngineIdle()
	d.engineMutex.Lock()
	defer d.engineMutex.Unlock()
	enqueue()
}

// AllocateUnifiedMemoryOnGPU fixes a new allocation's initial home. It does not
// migrate live data, and preserves Unified=true and demand mapping semantics.
func (d *Driver) AllocateUnifiedMemoryOnGPU(ctx *Context, bytes uint64, gpu int) Ptr {
	ptr := Ptr(d.memAllocator.AllocateUnifiedOnGPU(ctx.pid, bytes, gpu))
	ctx.buffers = append(ctx.buffers, &buffer{vAddr: ptr, size: bytes})
	return ptr
}
