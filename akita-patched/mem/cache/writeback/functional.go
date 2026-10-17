package writeback

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

// FunctionalWritebackRange publishes dirty bytes directly to backing storage,
// without a request, cache-state transition, scheduling, or simulated time.
// The range must cover complete cache lines (managed pages satisfy this).
func FunctionalWritebackRange(c *Comp, start, size uint64, write func(uint64, []byte) error) uint64 {
	var n uint64
	width := uint64(1) << c.Spec().Log2BlockSize
	for si := range c.State.DirectoryState.Sets {
		for wi := range c.State.DirectoryState.Sets[si].Blocks {
			b := &c.State.DirectoryState.Sets[si].Blocks[wi]
			if !b.IsValid || !b.IsDirty || b.Tag < start || b.Tag-start >= size {
				continue
			}
			data, err := c.Resources().Storage.Read(b.CacheAddress, width)
			if err != nil {
				panic(err)
			}
			// Preserve byte masks when a dirty line contains only a partial write.
			if len(b.DirtyMask) == 0 {
				if err := write(b.Tag, data); err != nil {
					panic(err)
				}
			} else {
				for lo := 0; lo < len(data); {
					if !b.DirtyMask[lo] {
						lo++
						continue
					}
					hi := lo + 1
					for hi < len(data) && b.DirtyMask[hi] {
						hi++
					}
					if err := write(b.Tag+uint64(lo), data[lo:hi]); err != nil {
						panic(err)
					}
					lo = hi
				}
			}
			b.IsDirty = false
			b.DirtyMask = nil
			n++
		}
	}
	return n
}
