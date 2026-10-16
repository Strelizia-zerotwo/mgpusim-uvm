package stencil2d

import "github.com/sarchlab/mgpusim/v5/amd/driver"

// Page ownership is static and determined before initialization. No data moves
// during execution. With aligned interior boundaries every write is owner-local;
// otherwise a shared boundary page goes to the owner of the most bytes (ties
// use row-partition order), and remaining remote writes are explicitly counted.
func (b *ParallelBenchmark) allocateGrid(bytes uint64) driver.Ptr {
	page := uint64(1) << b.d.Log2PageSize
	total := bytes + b.gridPrefix
	var base driver.Ptr
	for off := uint64(0); off < total; off += page {
		gpu := b.gpus[0]
		switch b.HomePlacement {
		case "", "owner":
			gpu = b.pageOwner(off, page)
		case "gpu1":
			gpu = 1
		default:
			panic("parallel stencil: unknown home placement")
		}
		ptr := b.d.AllocateUnifiedMemoryOnGPU(b.ctx, page, gpu)
		if off == 0 {
			base = ptr
		} else if uint64(ptr) != uint64(base)+off {
			panic("parallel stencil: grid pages must have contiguous virtual addresses")
		}
	}
	b.gridBases = append(b.gridBases, base)
	b.gridSizes = append(b.gridSizes, total)
	return base + driver.Ptr(b.gridPrefix)
}
func (b *ParallelBenchmark) pageOwner(off, page uint64) int {
	owned := (b.Rows - 2) / len(b.gpus)
	rowBytes := uint64(b.pitch * 4)
	best, most := b.gpus[0], uint64(0)
	for i, gpu := range b.gpus {
		lo, hi := 1+i*owned, 1+(i+1)*owned
		if i == 0 {
			lo = 0
		}
		if i == len(b.gpus)-1 {
			hi = b.Rows
		}
		start := max(off, b.gridPrefix+uint64(lo)*rowBytes)
		end := min(off+page, b.gridPrefix+uint64(hi)*rowBytes)
		if end > start && end-start > most {
			best, most = gpu, end-start
		}
	}
	return best
}
