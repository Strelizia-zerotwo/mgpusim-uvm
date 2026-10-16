package driver

// ReplicaHooks is used only by the explicit core benchmark oracle adapter.
// Kernel hooks submit one whole unified launch and drain it before returning.
type ReplicaHooks struct {
	Allocate  func(*Context, uint64) Ptr
	Kernel    func(*CommandQueue, func())
	HostWrite func(*CommandQueue, Ptr, interface{}, func()) bool
	Free      func(*Context, Ptr) bool
}

// RetireReplicaBuffer marks every chunk of a managed allocation dead. The
// session has already drained caches, removed mappings and reclaimed frames.
func (d *Driver) RetireReplicaBuffer(ctx *Context, ptr Ptr, bytes uint64) {
	for _, b := range ctx.buffers {
		if b.vAddr >= ptr && uint64(b.vAddr) < uint64(ptr)+bytes {
			b.freed = true
		}
	}
}
