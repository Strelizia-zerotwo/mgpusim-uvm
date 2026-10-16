package main

import (
	"encoding/binary"
	"fmt"
	"github.com/sarchlab/mgpusim/v5/amd/driver"
	"github.com/sarchlab/mgpusim/v5/amd/samples/runner"
	"log"
)

type probe struct {
	variant    string
	pages      int
	r          *runner.Runner
	gpus       []int
	ready      func(*driver.Context, []driver.Ptr, []uint64)
	epoch      func(string, func())
	pageBytes  uint64
	ctx        *driver.Context
	data, seed driver.Ptr
	outs       map[int]driver.Ptr
	expected   []uint32
}

func (b *probe) SelectGPU(ids []int) { b.gpus = ids }
func (*probe) SetUnifiedMemory()     {}
func (b *probe) Run() {
	if b.pages < 1 || b.pages > 2 {
		panic("probe-pages must be 1 or 2")
	}
	if len(b.gpus) < 2 {
		panic("probe requires at least two independent GPUs")
	}
	if b.variant != "standard" && b.variant != "cold-write" && b.variant != "fallback" {
		panic("unknown probe-case")
	}
	b.setup()
	seed := make([]uint32, 8)
	for i := range seed {
		seed[i] = uint32(9000 + i)
	}
	if b.variant != "cold-write" {
		b.epoch("cold-read", func() { b.readers(0, int(b.pageBytes)) })
		b.verifyRange(0, int(b.pageBytes))
	}
	b.epoch("write-and-clean-read", func() { b.write(0); b.readers(512, 512) })
	copy(b.expected[:8], seed)
	b.verifyRange(512, 512)
	b.epoch("clean-reuse", func() { b.readers(512, 512) })
	b.verifyRange(512, 512)
	b.epoch("dirty-reuse", func() { b.readers(0, 32) })
	b.verifyRange(0, 32)
	b.epoch("second-write", func() { b.write(64) })
	copy(b.expected[16:24], seed)
	b.epoch("third-write", func() { b.write(128) })
	copy(b.expected[32:40], seed)
	b.epoch("merged-repair", func() { b.readers(0, int(b.pageBytes)) })
	b.verifyRange(0, int(b.pageBytes))
	if b.variant == "fallback" {
		b.fallbackCases(seed)
	}
	if b.pages == 2 {
		b.epoch("other-page", func() { b.readers(int(b.pageBytes), int(b.pageBytes)) })
		b.verifyRange(int(b.pageBytes), int(b.pageBytes))
		b.epoch("return-page", func() { b.readers(0, int(b.pageBytes)) })
		b.verifyRange(0, int(b.pageBytes))
	}
}
func (b *probe) setup() {
	d := b.r.Driver()
	b.ctx = d.Init()
	d.SelectGPU(b.ctx, 1)
	b.data = d.AllocateUnifiedMemory(b.ctx, b.pageBytes*uint64(b.pages))
	b.seed = d.AllocateMemory(b.ctx, 32)
	b.expected = make([]uint32, b.pageBytes*uint64(b.pages)/4)
	for i := range b.expected {
		b.expected[i] = uint32(i + 1)
	}
	d.MemCopyH2D(b.ctx, b.data, b.expected)
	seed := make([]uint32, 8)
	for i := range seed {
		seed[i] = uint32(9000 + i)
	}
	d.MemCopyH2D(b.ctx, b.seed, seed)
	b.outs = map[int]driver.Ptr{}
	for _, g := range b.gpus {
		d.SelectGPU(b.ctx, g)
		b.outs[g] = d.AllocateMemory(b.ctx, b.pageBytes)
	}
	b.ready(b.ctx, []driver.Ptr{b.data}, []uint64{b.pageBytes * uint64(b.pages)})
}
func (b *probe) write(offset int) {
	d := b.r.Driver()
	d.SelectGPU(b.ctx, b.gpus[0])
	d.MemCopyD2D(b.ctx, b.data+driver.Ptr(offset), b.seed, 32)
}
func (b *probe) readers(offset, n int) {
	d := b.r.Driver()
	var qs []*driver.CommandQueue
	d.EnqueueAtBarrier(func() {
		for _, g := range b.gpus[1:] {
			d.SelectGPU(b.ctx, g)
			q := d.CreateCommandQueue(b.ctx)
			d.EnqueueMemCopyD2D(q, b.outs[g], b.data+driver.Ptr(offset), n)
			qs = append(qs, q)
		}
	})
	for _, q := range qs {
		d.DrainCommandQueue(q)
	}
}
func (b *probe) verifyRange(offset, n int) {
	d := b.r.Driver()
	d.WaitForEngineIdle()
	for _, g := range b.gpus[1:] {
		for i := 0; i < n/4; i++ {
			addr := uint64(b.outs[g]) + uint64(i*4)
			p, ok := d.Resources().PageTable.Find(b.ctx.PID(), addr)
			if !ok {
				panic("missing output allocation")
			}
			raw, err := d.Resources().GlobalStorage.Read(p.PAddr+addr-p.VAddr, 4)
			check(err)
			v := binary.LittleEndian.Uint32(raw)
			if v != b.expected[offset/4+i] {
				panic(fmt.Sprintf("probe GPU %d offset %d: got %d want %d", g, offset+i*4, v, b.expected[offset/4+i]))
			}
		}
	}
}
func (*probe) Verify() {
	log.Print("Passed! replica probe: cold copy, clean read, dirty repair and merged writes verified")
}

// fallbackCases has disjoint logical writes on a shared page and disjoint
// byte ranges whose 64B read transaction conservatively overlaps a write.
func (b *probe) fallbackCases(seed []uint32) {
	d := b.r.Driver()
	b.epoch("multiple-writers", func() {
		var qs []*driver.CommandQueue
		for i, g := range b.gpus[:2] {
			d.SelectGPU(b.ctx, g)
			q := d.CreateCommandQueue(b.ctx)
			d.EnqueueMemCopyD2D(q, b.data+driver.Ptr(256+i*64), b.seed, 32)
			qs = append(qs, q)
		}
		for _, q := range qs {
			d.DrainCommandQueue(q)
		}
	})
	copy(b.expected[64:72], seed)
	copy(b.expected[80:88], seed)
	b.epoch("after-multiple-writers", func() { b.readers(0, int(b.pageBytes)) })
	b.verifyRange(0, int(b.pageBytes))
	b.epoch("conservative-read-overlap", func() { b.write(512); b.readers(544, 32) })
	copy(b.expected[128:136], seed)
	b.verifyRange(544, 32)
	b.epoch("after-overlap", func() { b.readers(0, int(b.pageBytes)) })
	b.verifyRange(0, int(b.pageBytes))
}
