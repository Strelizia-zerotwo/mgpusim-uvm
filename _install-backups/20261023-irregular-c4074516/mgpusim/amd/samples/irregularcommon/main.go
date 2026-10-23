package irregularcommon

import (
	"flag"
	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/irregular"
	"github.com/sarchlab/mgpusim/v5/amd/samples/runner"
)

func Run(algorithm string) {
	nodes := flag.Int("node", 4096, "Graph vertices or pointer nodes")
	degree := flag.Int("degree", 6, "Generated graph degree / copy constraints per pointer")
	components := flag.Int("components", 8, "SCC components or Louvain disconnected input regions")
	kind := flag.String("graph", "random", "Generated input: random or grid")
	seed := flag.Int64("seed", 42, "Deterministic input seed")
	rounds := flag.Int("max-rounds", 4096, "Hard failure budget; no unconverged result accepted")
	sweeps := flag.Int("sweeps", 4, "Complete SymGS forward/backward sweeps")
	objects := flag.Int("objects", 64, "PTA heap objects; also dereferenceable pointer nodes")
	stores := flag.Int("stores", 16, "PTA store constraints")
	alpha := flag.Float64("alpha", 0.15, "PPR restart parameter, 0<alpha<1")
	epsilon := flag.Float64("epsilon", 0.0001, "PPR degree-scaled residual threshold")
	points := flag.Int("points", 16, "DMR initial square points, including four corners")
	insertions := flag.Int("max-insertions", 128, "DMR refinement insertion capacity; failure if insufficient")
	area := flag.Float64("max-area", 0.04, "DMR maximum final triangle area; NOT a minimum-angle criterion")
	flag.Parse()
	r := new(runner.Runner).Init()
	b := irregular.NewBenchmark(r.Driver(), algorithm)
	b.Arch = r.ArchType
	b.Nodes = *nodes
	b.Degree = *degree
	b.Components = *components
	b.GraphKind = *kind
	b.Seed = *seed
	b.MaxRounds = *rounds
	b.Sweeps = *sweeps
	b.Objects = *objects
	b.Stores = *stores
	b.Alpha = float32(*alpha)
	b.Epsilon = float32(*epsilon)
	b.Points = *points
	b.MaxInsertions = *insertions
	b.MaxArea = float32(*area)
	r.AddBenchmark(b)
	r.Run()
}
