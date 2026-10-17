package writethroughcache

// FunctionalInvalidateRange is an ideal, zero-time coherence operation. It
// changes directory validity only: pipeline ownership, locks and read counts
// remain intact so already issued traffic can complete normally. Ideal AT
// response snapshots handle old in-flight fills without draining the cache.
func FunctionalInvalidateRange(c *Comp, start, size uint64) uint64 {
	var n uint64
	for si := range c.State.DirectoryState.Sets {
		for wi := range c.State.DirectoryState.Sets[si].Blocks {
			b := &c.State.DirectoryState.Sets[si].Blocks[wi]
			if b.Tag < start || b.Tag-start >= size || !b.IsValid {
				continue
			}
			b.IsValid = false
			b.IsDirty = false
			b.DirtyMask = nil
			n++
		}
	}
	return n
}
