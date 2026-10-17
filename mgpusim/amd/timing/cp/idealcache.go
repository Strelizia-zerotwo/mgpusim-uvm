package cp

import "sync"

var idealKernelStarts sync.Map

// SetIdealKernelStart installs optional zero-time cache handling for ideal
// experiments. A nil callback restores the ordinary cache-control sequence.
func SetIdealKernelStart(c *Comp, f func()) {
	if f == nil {
		idealKernelStarts.Delete(c)
	} else {
		idealKernelStarts.Store(c, f)
	}
}
func idealKernelStart(c *Comp) bool {
	if f, ok := idealKernelStarts.Load(c); ok {
		f.(func())()
		return true
	}
	return false
}
