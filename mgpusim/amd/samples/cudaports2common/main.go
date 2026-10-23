// Package cudaportscommon supplies the shared CLI for CUDA-derived workloads.
package cudaports2common

import (
	"flag"

	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/cudaports2"
	"github.com/sarchlab/mgpusim/v5/amd/samples/runner"
)

func Run(algorithm string) {
	componentsDefault := 1
	if algorithm == "CC" {
		componentsDefault = 4
	}
	nodes := flag.Int("node", 16384, "Number of graph vertices")
	degree := flag.Int("degree", 8, "Approximate undirected degree for random graphs")
	components := flag.Int("components", componentsDefault, "Disconnected graph components (grid requires 1)")
	graph := flag.String("graph", "random", "Input graph: random or grid; both generated, not real datasets")
	seed := flag.Int64("seed", 42, "Deterministic input and priority seed")
	rounds := flag.Int("max-rounds", 4096, "Fail rather than accept an unconverged graph result")
	rows := flag.Int("rows", 8192, "Embedding table rows")
	dim := flag.Int("dim", 32, "Embedding dimension, a power of two <=256")
	batch := flag.Int("batch", 128, "Global lookups per minibatch, 1..256; GPU grouping is timed")
	batches := flag.Int("batches", 8, "Number of lookup and sparse SGD minibatches")
	distribution := flag.String("distribution", "hotset", "Embedding indices: uniform or hotset")
	rate := flag.Float64("learning-rate", 0.1, "SGD learning rate (gradient is averaged over global batch)")
	flag.Parse()
	r := new(runner.Runner).Init()
	b := cudaports2.NewBenchmark(r.Driver(), algorithm)
	b.Arch = r.ArchType
	b.Nodes = *nodes
	b.Degree = *degree
	b.Components = *components
	b.GraphKind = *graph
	b.Seed = *seed
	b.MaxRounds = *rounds
	b.Rows = *rows
	b.Dim = *dim
	b.Batch = *batch
	b.Batches = *batches
	b.Distribution = *distribution
	b.LearningRate = float32(*rate)
	r.AddBenchmark(b)
	r.Run()
}
