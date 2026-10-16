package localmapping

import "github.com/sarchlab/akita/v5/mem/vm"

// InstallAtBarrier publishes a per-GPU physical replica view. This privileged
// oracle operation requires drained queues, flushed caches and invalidated TLBs.
// It leaves the authoritative allocation and the demand-mode Install guard intact.
func (v *View) InstallAtBarrier(page vm.Page) {
	v.owner.mu.Lock()
	defer v.owner.mu.Unlock()
	master, ok := v.owner.master.Find(page.PID, page.VAddr)
	if !ok || !master.Unified || !page.Valid || page.IsMigrating || page.PageSize != master.PageSize {
		panic("replica: unusable barrier mapping")
	}
	if _, ok := v.table.Find(page.PID, page.VAddr); ok {
		v.table.Update(page)
	} else {
		v.table.Insert(page)
	}
}
