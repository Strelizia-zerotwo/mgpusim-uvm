// Package irregular provides seven explicitly documented algorithmic ports.
// All policy behavior belongs to the existing driver/AT replica hooks.
package irregular

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"

	"github.com/sarchlab/mgpusim/v5/amd/arch"
	"github.com/sarchlab/mgpusim/v5/amd/driver"
	"github.com/sarchlab/mgpusim/v5/amd/insts"
)

const block = 256
const none = ^uint32(0)

// deviceBackend permits the same controller to be validated with compiled
// instruction emulation; production uses the ordinary Driver below.
type deviceBackend interface {
	Allocate(int) driver.Ptr
	Upload(driver.Ptr, interface{})
	Download(driver.Ptr, interface{})
	Launch(string, int, Args)
}

const Implementation = "irregular-algorithmic-amd-v1"

//go:embed kernels_gfx942.hsaco
var code []byte

// Every kernel uses this explicit ABI, followed by compiler hidden arguments.
type Args struct {
	A      [12]driver.Ptr
	P      [8]uint32
	F      [4]float32
	Hidden [128]byte
}
type Benchmark struct {
	backend                                                                              deviceBackend
	d                                                                                    *driver.Driver
	ctx                                                                                  *driver.Context
	queue                                                                                *driver.CommandQueue
	gpus                                                                                 []int
	unified                                                                              bool
	Arch                                                                                 arch.Type
	Algorithm                                                                            string
	Nodes, Degree, Components, MaxRounds, Sweeps, Objects, Stores, Points, MaxInsertions int
	Seed                                                                                 int64
	GraphKind                                                                            string
	Alpha, Epsilon, MaxArea                                                              float32
	kernels                                                                              map[string]*insts.KernelCodeObject
	metadata                                                                             map[string]interface{}
	verify                                                                               func() error
}

func NewBenchmark(d *driver.Driver, algorithm string) *Benchmark {
	return &Benchmark{d: d, ctx: d.Init(), Algorithm: algorithm, Nodes: 4096, Degree: 6, Components: 8,
		MaxRounds: 4096, Sweeps: 4, Objects: 64, Stores: 16, Points: 16, MaxInsertions: 128,
		Seed: 42, GraphKind: "random", Alpha: 0.15, Epsilon: 0.0001, MaxArea: 0.04,
		kernels: map[string]*insts.KernelCodeObject{}}
}
func (b *Benchmark) SelectGPU(g []int) { b.gpus = g }
func (b *Benchmark) SetUnifiedMemory() { b.unified = true }
func (b *Benchmark) alloc(bytes int) driver.Ptr {
	if bytes < 4 {
		bytes = 4
	}
	if b.backend != nil {
		return b.backend.Allocate(bytes)
	}
	if b.unified {
		return b.d.AllocateUnifiedMemory(b.ctx, uint64(bytes))
	}
	return b.d.AllocateMemory(b.ctx, uint64(bytes))
}
func (b *Benchmark) u32(v []uint32) driver.Ptr {
	p := b.alloc(len(v) * 4)
	if len(v) > 0 {
		b.write(p, v)
	}
	return p
}
func (b *Benchmark) floats(v []float32) driver.Ptr {
	p := b.alloc(len(v) * 4)
	if len(v) > 0 {
		b.write(p, v)
	}
	return p
}
func (b *Benchmark) read(p driver.Ptr, n int) []uint32 {
	v := make([]uint32, n)
	if n > 0 {
		b.download(p, v)
	}
	return v
}
func (b *Benchmark) readFloat(p driver.Ptr, n int) []float32 {
	v := make([]float32, n)
	if n > 0 {
		b.download(p, v)
	}
	return v
}
func (b *Benchmark) write(p driver.Ptr, v interface{}) {
	if b.backend != nil {
		b.backend.Upload(p, v)
	} else {
		b.d.MemCopyH2D(b.ctx, p, v)
	}
}
func (b *Benchmark) download(p driver.Ptr, v interface{}) {
	if b.backend != nil {
		b.backend.Download(p, v)
	} else {
		b.d.MemCopyD2H(b.ctx, v, p)
	}
}
func (b *Benchmark) launch(name string, extent int, args Args) {
	if b.backend != nil {
		b.backend.Launch(name, extent, args)
		b.metadata["kernel_launches"] = b.metadata["kernel_launches"].(int) + 1
		return
	}
	if extent < 1 {
		panic("zero launch extent")
	}
	kernel := b.kernels[name]
	if kernel == nil {
		kernel = insts.LoadKernelCodeObjectFromBytes(code, name)
		if kernel == nil {
			panic("missing kernel " + name)
		}
		b.kernels[name] = kernel
	}
	b.d.EnqueueLaunchKernel(b.queue, kernel, [3]uint32{uint32((extent + block - 1) / block * block), 1, 1}, [3]uint16{block, 1, 1}, &args)
	b.d.DrainCommandQueue(b.queue)
	b.metadata["kernel_launches"] = b.metadata["kernel_launches"].(int) + 1
}
func (b *Benchmark) progress(p driver.Ptr, n int) int {
	sum := 0
	for _, x := range b.read(p, (n+block-1)/block) {
		sum += int(x)
	}
	return sum
}
func (b *Benchmark) until(name string, n int, a Args, counts driver.Ptr) int {
	for i := 1; i <= b.MaxRounds; i++ {
		b.launch(name, n, a)
		changed := b.progress(counts, n)
		log.Printf("IRREGULAR %s %s round=%d changed=%d", b.Algorithm, name, i, changed)
		if changed == 0 {
			return i
		}
	}
	panic(fmt.Sprintf("%s %s did not converge in %d rounds", b.Algorithm, name, b.MaxRounds))
}
func (b *Benchmark) Run() {
	if b.Arch != arch.CDNA3 {
		panic("irregular benchmarks require -arch=cdna3 -gpu=mi300x")
	}
	if len(b.gpus) != 1 {
		panic("use one physical or unified target device")
	}
	if b.Nodes < 1 || b.MaxRounds < 1 {
		panic("node and max-rounds must be positive")
	}
	b.d.SelectGPU(b.ctx, b.gpus[0])
	b.queue = b.d.CreateCommandQueue(b.ctx)
	b.metadata = map[string]interface{}{"format": 1, "algorithm": b.Algorithm, "implementation": Implementation,
		"seed": b.Seed, "kernel_launches": 0, "policy_independent_input": true,
		"fidelity_document": "benchmark/mybenchmark/irregular/FIDELITY.md"}
	switch b.Algorithm {
	case "SCC":
		b.runSCC()
	case "PPR":
		b.runPPR()
	case "SYMGS", "LP":
		b.runColored()
	case "PTA":
		b.runPTA()
	case "LOUVAIN":
		b.runLouvain()
	case "DMR":
		b.runMesh()
	default:
		panic("unknown irregular algorithm " + b.Algorithm)
	}
	b.metadata["converged"] = true
	data, err := json.MarshalIndent(b.metadata, "", "  ")
	if err != nil {
		panic(err)
	}
	if err = os.WriteFile("workload.json", append(data, '\n'), 0644); err != nil {
		panic(err)
	}
}
func (b *Benchmark) Verify() {
	if b.verify == nil {
		panic("missing independent numerical verifier")
	}
	if err := b.verify(); err != nil {
		panic(fmt.Sprintf("%s verification failed: %v", b.Algorithm, err))
	}
	log.Printf("Passed! %s independent algorithm/invariant verification", b.Algorithm)
}
func closeFloat(a, c float32, tolerance float64) bool {
	return !math.IsNaN(float64(c)) && !math.IsInf(float64(c), 0) && math.Abs(float64(a-c)) <= tolerance*(1+math.Abs(float64(a)))
}
