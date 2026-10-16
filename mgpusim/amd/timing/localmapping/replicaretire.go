package localmapping

import "github.com/sarchlab/akita/v5/mem/vm"

// RetireAtBarrier is explicit lifetime retirement, never a live remap. The
// replica session must have globally drained caches and invalidated all TLBs.
func (t *Tables) RetireAtBarrier(pid vm.PID, addr uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.master.Remove(pid, addr)
	for _, v := range t.views {
		if _, ok := v.table.Find(pid, addr); ok {
			v.table.Remove(pid, addr)
		}
	}
}
