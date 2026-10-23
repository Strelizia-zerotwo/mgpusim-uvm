// Package cudaports2 contains in-place/grouped variants of public CUDA
// workloads. See SOURCES.md for provenance and changes from the original code.
package cudaports2

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"

	"github.com/sarchlab/mgpusim/v5/amd/arch"
	"github.com/sarchlab/mgpusim/v5/amd/driver"
	"github.com/sarchlab/mgpusim/v5/amd/insts"
)

const blockSize = 256
const infinity uint32 = 0x3fffffff

//go:embed kernels_gfx942.hsaco
var kernelBytes []byte

// The unused tails cover OpenCL hidden ABI arguments. Kernels use the explicit
// AMD workgroup/workitem builtins and never read hidden global offsets.
type GraphArgs struct {
	Row, Col, Weight, State, Priority, Counts driver.Ptr
	N, Mode, Iteration                        uint32
	Hidden                                    [128]byte
}
type MarkArgs struct {
	Row, Col, State, Priority driver.Ptr
	N                         uint32
	Hidden                    [128]byte
}
type DropArgs struct {
	Row, Col, State, Counts driver.Ptr
	N                       uint32
	Hidden                  [128]byte
}
type EmbeddingArgs struct {
	Table, IDs, Output, Order  driver.Ptr
	Dim, LogDim, Items, Offset uint32
	Step                       float32
	Hidden                     [128]byte
}
type GroupArgs struct {
	IDs, Order    driver.Ptr
	Items, Offset uint32
	Hidden        [128]byte
}

type Benchmark struct {
	driver                                 *driver.Driver
	context                                *driver.Context
	queue                                  *driver.CommandQueue
	gpus                                   []int
	useUnifiedMemory                       bool
	Arch                                   arch.Type
	Algorithm                              string
	Nodes, Degree, Components, MaxRounds   int
	GraphKind                              string
	Seed                                   int64
	Rows, Dim, Batch, Batches              int
	Distribution                           string
	LearningRate                           float32
	gather, publish, lookup, update, group *insts.KernelCodeObject
	graph                                  Graph
	priorities, initial, result            []uint32
	initialTable, finalTable               []float32
	indices                                []uint32
	rounds                                 int
}

func NewBenchmark(d *driver.Driver, algorithm string) *Benchmark {
	return &Benchmark{driver: d, context: d.Init(), Algorithm: algorithm,
		Nodes: 32768, Degree: 8, Components: 1, MaxRounds: 4096, GraphKind: "random", Seed: 42,
		Rows: 16384, Dim: 32, Batch: 128, Batches: 20, Distribution: "hotset", LearningRate: 0.1}
}
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }
func (b *Benchmark) SetUnifiedMemory()    { b.useUnifiedMemory = true }
func (b *Benchmark) allocate(bytes uint64) driver.Ptr {
	if bytes == 0 {
		bytes = 4
	}
	if b.useUnifiedMemory {
		return b.driver.AllocateUnifiedMemory(b.context, bytes)
	}
	return b.driver.AllocateMemory(b.context, bytes)
}
func kernel(name string) *insts.KernelCodeObject {
	k := insts.LoadKernelCodeObjectFromBytes(kernelBytes, name)
	if k == nil {
		panic("cannot load CUDA adaptation kernel: " + name)
	}
	return k
}
func (b *Benchmark) launch(k *insts.KernelCodeObject, count int, args interface{}) {
	b.driver.EnqueueLaunchKernel(b.queue, k, [3]uint32{uint32((count + blockSize - 1) / blockSize * blockSize), 1, 1}, [3]uint16{blockSize, 1, 1}, args)
	b.driver.DrainCommandQueue(b.queue)
}
func (b *Benchmark) mode() uint32 {
	switch b.Algorithm {
	case "SSSP":
		return 0
	case "CC":
		return 1
	case "COLOR":
		return 2
	case "MIS":
		return 3
	}
	panic("unknown graph algorithm: " + b.Algorithm)
}
func (b *Benchmark) Run() {
	if b.Arch != arch.CDNA3 {
		panic("CUDA adaptations require -arch cdna3 -gpu mi300x")
	}
	if len(b.gpus) != 1 {
		panic("select one GPU or the unified multi-GPU device")
	}
	b.initCommandQueue()
	if b.Algorithm == "EMBEDDING" {
		b.runEmbedding()
	} else {
		b.runGraph()
	}
}
func (b *Benchmark) initCommandQueue() {
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.queue = b.driver.CreateCommandQueue(b.context)
}
func (b *Benchmark) runGraph() {
	if b.MaxRounds < 1 {
		panic("max-rounds must be positive")
	}
	var err error
	b.graph, err = GenerateGraph(b.Nodes, b.Degree, b.Components, b.GraphKind, b.Seed)
	if err != nil {
		panic(err)
	}
	b.initial, b.priorities = initialState(b.Nodes, b.mode(), b.Seed)
	row := b.allocate(uint64(len(b.graph.Row) * 4))
	col := b.allocate(uint64(len(b.graph.Col) * 4))
	weight := b.allocate(uint64(len(b.graph.Weight) * 4))
	state := b.allocate(uint64(b.Nodes * 4))
	priority := b.allocate(uint64(b.Nodes * 4))
	groups := (b.Nodes + blockSize - 1) / blockSize
	counts := b.allocate(uint64(groups * 4))
	b.driver.MemCopyH2D(b.context, row, b.graph.Row)
	b.driver.MemCopyH2D(b.context, col, b.graph.Col)
	b.driver.MemCopyH2D(b.context, weight, b.graph.Weight)
	b.driver.MemCopyH2D(b.context, state, b.initial)
	b.driver.MemCopyH2D(b.context, priority, b.priorities)
	progress := make([]uint32, groups)
	if b.Algorithm == "MIS" {
		b.gather, b.publish = kernel("mis_mark"), kernel("mis_drop")
	} else {
		b.gather = kernel("graph_inplace")
	}
	remaining := b.Nodes
	for iteration := 0; iteration < b.MaxRounds; iteration++ {
		if b.Algorithm == "MIS" {
			b.launch(b.gather, b.Nodes, &MarkArgs{Row: row, Col: col, State: state, Priority: priority, N: uint32(b.Nodes)})
			b.launch(b.publish, b.Nodes, &DropArgs{Row: row, Col: col, State: state, Counts: counts, N: uint32(b.Nodes)})
		} else {
			b.launch(b.gather, b.Nodes, &GraphArgs{Row: row, Col: col, Weight: weight, State: state, Priority: priority, Counts: counts,
				N: uint32(b.Nodes), Mode: b.mode(), Iteration: uint32(iteration)})
		}
		b.driver.MemCopyD2H(b.context, progress, counts)
		total := 0
		for _, x := range progress {
			total += int(x)
		}
		if b.Algorithm == "MIS" {
			remaining = total
		} else if b.Algorithm == "COLOR" {
			remaining -= total
		}
		b.rounds = iteration + 1
		log.Printf("CUDA-PORT2 %s round=%d changed_or_remaining=%d", b.Algorithm, b.rounds, total)
		done := (b.mode() < 2 && total == 0) || (b.mode() >= 2 && remaining == 0)
		if done {
			b.result = make([]uint32, b.Nodes)
			b.driver.MemCopyD2H(b.context, b.result, state)
			b.writeMetadata(map[string]interface{}{"vertices": b.Nodes, "directed_edges": len(b.graph.Col),
				"graph_kind": b.GraphKind, "components_requested": b.Components, "rounds": b.rounds, "converged": true,
				"progress_counters": groups, "progress_representation": "GPU workgroup reduction; one uint32 per group",
				"active_set":                      "full vertex dispatch with inactive early exit; no compact frontier",
				"update_semantics":                "unique vertex writer, in-place state stores; SSSP/CC publish each improvement",
				"exact_edge_parallel_atomic_port": false})
			return
		}
		if b.Algorithm == "COLOR" && total == 0 {
			panic("coloring stopped before completion")
		}
	}
	panic(fmt.Sprintf("%s-2 did not converge in %d rounds", b.Algorithm, b.MaxRounds))
}
func initialState(n int, mode uint32, seed int64) ([]uint32, []uint32) {
	s, p := make([]uint32, n), make([]uint32, n)
	rng := rand.New(rand.NewSource(seed + 17))
	for v := range s {
		p[v] = rng.Uint32()
		switch mode {
		case 0, 2:
			s[v] = infinity
		case 1:
			s[v] = uint32(v)
		}
	}
	if mode == 0 {
		s[0] = 0
	}
	return s, p
}
func (b *Benchmark) prepareEmbedding() error {
	if b.Rows < 1 || b.Batch < 1 || b.Batch > 256 || b.Batches < 1 || b.Dim < 1 || b.Dim > 256 || b.Dim&(b.Dim-1) != 0 {
		return fmt.Errorf("rows/batches positive, batch in [1,256]; dim a power of two <=256")
	}
	if b.Distribution != "uniform" && b.Distribution != "hotset" {
		return fmt.Errorf("distribution must be uniform or hotset")
	}
	if b.LearningRate <= 0 || math.IsNaN(float64(b.LearningRate)) || math.IsInf(float64(b.LearningRate), 0) {
		return fmt.Errorf("learning rate must be finite and positive")
	}
	rng := rand.New(rand.NewSource(b.Seed))
	b.initialTable = make([]float32, b.Rows*b.Dim)
	for i := range b.initialTable {
		b.initialTable[i] = float32(rng.Intn(1024)) / 1024
	}
	perm := rng.Perm(b.Rows)
	hot := b.Rows / 64
	if hot < 1 {
		hot = 1
	}
	b.indices = make([]uint32, b.Batch*b.Batches)
	for i := range b.indices {
		rank := rng.Intn(b.Rows)
		if b.Distribution == "hotset" && rng.Intn(10) < 8 {
			rank = rng.Intn(hot)
		}
		b.indices[i] = uint32(perm[rank])
	}
	return nil
}
func logDimension(dim int) uint32 {
	var x uint32
	for 1<<x < dim {
		x++
	}
	return x
}
func (b *Benchmark) runEmbedding() {
	if err := b.prepareEmbedding(); err != nil {
		panic(err)
	}
	b.lookup, b.update, b.group = kernel("embedding_lookup"), kernel("embedding_update"), kernel("embedding_group")
	table := b.allocate(uint64(len(b.initialTable) * 4))
	ids := b.allocate(uint64(len(b.indices) * 4))
	out := b.allocate(uint64(b.Batch * b.Dim * 4))
	order := b.allocate(uint64(b.Batch * 4))
	b.driver.MemCopyH2D(b.context, table, b.initialTable)
	b.driver.MemCopyH2D(b.context, ids, b.indices)
	for batch := 0; batch < b.Batches; batch++ {
		a := &EmbeddingArgs{Table: table, IDs: ids, Output: out, Order: order, Dim: uint32(b.Dim), LogDim: logDimension(b.Dim), Items: uint32(b.Batch), Offset: uint32(batch * b.Batch), Step: b.LearningRate / float32(b.Batch)}
		b.launch(b.lookup, b.Batch*b.Dim, a)
		b.launch(b.group, 256, &GroupArgs{IDs: ids, Order: order, Items: uint32(b.Batch), Offset: uint32(batch * b.Batch)})
		b.launch(b.update, b.Batch*b.Dim, a)
		log.Printf("CUDA-PORT EMBEDDING batch=%d/%d", batch+1, b.Batches)
	}
	b.rounds = b.Batches
	b.finalTable = make([]float32, len(b.initialTable))
	b.driver.MemCopyD2H(b.context, b.finalTable, table)
	b.writeMetadata(map[string]interface{}{"rows": b.Rows, "dimension": b.Dim, "items_per_batch_global": b.Batch, "batches": b.Batches, "distribution": b.Distribution, "learning_rate": b.LearningRate, "converged": true, "grouping": "timed GPU stable bitonic sort, then per-run gradient reduction", "grouping_max_batch": 256})
}
func (b *Benchmark) writeMetadata(fields map[string]interface{}) {
	if b.Algorithm != "EMBEDDING" {
		fields["vertex_layout"] = "seeded-permutation"
		if b.GraphKind == "grid" {
			fields["vertex_layout"] = "grid-row-major"
		}
	}
	fields["algorithm"] = b.Algorithm + "-2"
	fields["algorithm_family"] = b.Algorithm
	fields["seed"] = b.Seed
	fields["implementation"] = "cuda-derived-inplace-grouped-v2"
	fields["atomic_operations"] = false
	fields["protocol_trigger"] = "actual in-kernel requests; no boundary replay"
	data, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		panic(err)
	}
	if err = os.WriteFile("workload.json", append(data, '\n'), 0644); err != nil {
		panic(err)
	}
}
func (b *Benchmark) Verify() {
	if b.Algorithm == "EMBEDDING" {
		want := embeddingReference(b.initialTable, b.indices, b.Dim, b.Batch, b.Batches, b.LearningRate)
		if len(b.finalTable) != len(want) {
			panic("embedding output missing")
		}
		for i, x := range want {
			if math.IsNaN(float64(b.finalTable[i])) || math.Abs(float64(b.finalTable[i]-x)) > 2e-5 {
				panic(fmt.Sprintf("embedding[%d]: got %g want %g", i, b.finalTable[i], x))
			}
		}
	} else if err := verifyGraph(b.graph, b.Algorithm, b.result); err != nil {
		panic(err)
	}
	log.Printf("Passed! %s CPU reference/structural verification", b.Algorithm)
}
func embeddingReference(initial []float32, ids []uint32, dim, batch, batches int, rate float32) []float32 {
	table := append([]float32(nil), initial...)
	for k := 0; k < batches; k++ {
		grad := map[uint32][]float32{}
		for _, id := range ids[k*batch : (k+1)*batch] {
			if grad[id] == nil {
				grad[id] = make([]float32, dim)
			}
			for d := 0; d < dim; d++ {
				grad[id][d] += table[int(id)*dim+d] - 0.125
			}
		}
		for id, g := range grad {
			for d, x := range g {
				table[int(id)*dim+d] -= (rate / float32(batch)) * x
			}
		}
	}
	return table
}
