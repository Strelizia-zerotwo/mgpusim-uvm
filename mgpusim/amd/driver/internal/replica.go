package internal

// AllocateReplicaFrame reserves a frame for a quiescent oracle experiment.
func (d *Device) AllocateReplicaFrame() uint64 { return d.allocatePage() }

// ReleaseReplicaFrame returns a previously reserved oracle frame.
func (d *Device) ReleaseReplicaFrame(addr uint64) { d.MemState.addSinglePAddr(addr) }
