package cp

import "testing"

func TestIdealKernelStartDoesNotEnqueueCacheControls(t *testing.T) {
	c := &Comp{}
	calls := 0
	SetIdealKernelStart(c, func() { calls++ })
	defer SetIdealKernelStart(c, nil)
	c.State.CtrlSeq = ctrlSeqKernelStart
	n := (&ctrlMiddleware{comp: c}).execKernelStartStep(0)
	if n != 0 || calls != 1 || c.State.CtrlSeq != ctrlSeqNone || len(c.State.PendingCacheReqs) != 0 {
		t.Fatal("ideal start queued coherence traffic")
	}
	SetIdealKernelStart(c, nil)
	if idealKernelStart(c) {
		t.Fatal("callback was not detached")
	}
}
