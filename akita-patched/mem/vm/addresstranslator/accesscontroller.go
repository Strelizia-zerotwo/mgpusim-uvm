package addresstranslator

import (
	"github.com/sarchlab/akita/v5/mem/memprotocol"
	"github.com/sarchlab/akita/v5/mem/vm"
	"sync"
)

// AccessController checks every access before translation/cache admission.
// Admit may block without consuming the request. Resolve supplies the physical
// frame leased at admission, including on a TLB hit. Complete releases the
// lease only after the memory response has been accepted upstream.
// Controllers are optional and require a serial simulation engine.
type AccessController interface {
	Admit(*Comp, memprotocol.AccessReq) bool
	Resolve(*Comp, uint64, vm.Page) vm.Page
	Complete(*Comp, uint64)
}

var accessControllers sync.Map

func SetAccessController(c *Comp, controller AccessController) {
	if controller == nil {
		accessControllers.Delete(c)
	} else {
		accessControllers.Store(c, controller)
	}
}
func accessController(c *Comp) AccessController {
	if v, ok := accessControllers.Load(c); ok {
		return v.(AccessController)
	}
	return nil
}

// DeferredAccessController lets the ideal model hold individual requests
// outside the Top FIFO while other accesses continue through the translator.
// The deferred list is simulator bookkeeping, not a modeled hardware queue.
type DeferredAccessController interface{ DeferBlockedAccess() bool }

// ReadDataController supplies a zero-time functional read snapshot. Timing
// still traverses ordinary caches/RDMA, including contention and cache misses.
type ReadDataController interface {
	ReadData(*Comp, uint64, []byte) []byte
}
