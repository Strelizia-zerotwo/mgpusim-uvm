package cudaports2

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/sarchlab/akita/v5/mem"
	v1 "github.com/sarchlab/mgpusim/v5/amd/benchmarks/cudaports"
	"reflect"
)

func TestV2PreservesGeneratedGraphAndRejectsEmbeddingOverflow(t *testing.T) {
	for _, kind := range []string{"random", "grid"} {
		components := 1
		if kind == "random" {
			components = 4
		}
		a, err := GenerateGraph(256, 8, components, kind, 42)
		if err != nil {
			t.Fatal(err)
		}
		b, err := v1.GenerateGraph(256, 8, components, kind, 42)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a.Row, b.Row) || !reflect.DeepEqual(a.Col, b.Col) || !reflect.DeepEqual(a.Weight, b.Weight) {
			t.Fatal("graph differs from v1")
		}
	}
	a := &Benchmark{Rows: 8192, Dim: 32, Batch: 128, Batches: 8, Seed: 42, Distribution: "hotset", LearningRate: 0.1}
	if err := a.prepareEmbedding(); err != nil {
		t.Fatal(err)
	}
	// Reject unsupported batches rather than silently sort/truncate them.
	a.Batch = 257
	if a.prepareEmbedding() == nil {
		t.Fatal("accepted grouping overflow")
	}
}

func TestCompiledV2Graphs(t *testing.T) {
	for mode, algorithm := range []string{"SSSP", "CC", "COLOR", "MIS"} {
		for _, n := range []int{17, 257} {
			for _, reverse := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/reverse=%v", algorithm, n, reverse), func(t *testing.T) {
					g, err := GenerateGraph(n, 8, 3, "random", 42)
					if err != nil {
						t.Fatal(err)
					}
					state, priority := initialState(n, uint32(mode), 42)
					if n == 17 {
						for i := range priority {
							priority[i] = 1
						}
					}
					s := testStorage{storage: mem.NewStorage(16 << 20), reverse: reverse}
					s.put(t, 0x100000, g.Row)
					s.put(t, 0x200000, g.Col)
					s.put(t, 0x300000, g.Weight)
					s.put(t, 0x400000, state)
					s.put(t, 0x500000, priority)
					remaining, done := n, false
					for iteration := 0; iteration <= n; iteration++ {
						if mode == 3 {
							s.launch(t, "mis_mark", &MarkArgs{Row: 0x100000, Col: 0x200000, State: 0x400000, Priority: 0x500000, N: uint32(n)}, n)
							s.launch(t, "mis_drop", &DropArgs{Row: 0x100000, Col: 0x200000, State: 0x400000, Counts: 0x600000, N: uint32(n)}, n)
						} else {
							s.launch(t, "graph_inplace", &GraphArgs{Row: 0x100000, Col: 0x200000, Weight: 0x300000, State: 0x400000, Priority: 0x500000, Counts: 0x600000, N: uint32(n), Mode: uint32(mode), Iteration: uint32(iteration)}, n)
						}
						counts := make([]uint32, (n+255)/256)
						s.get(t, 0x600000, counts, len(counts)*4)
						total := 0
						for _, x := range counts {
							total += int(x)
						}
						if mode == 3 {
							remaining = total
						} else if mode == 2 {
							remaining -= total
						}
						if (mode < 2 && total == 0) || (mode >= 2 && remaining == 0) {
							done = true
							break
						}
					}
					if !done {
						t.Fatal("did not converge")
					}
					s.get(t, 0x400000, state, n*4)
					if err := verifyGraph(g, algorithm, state); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}

func TestColorIncludesNeighboursColoredEarlierInSameRound(t *testing.T) {
	// The last workgroup runs first and colors vertex 256 with color 0.
	// Vertex 0 must still include that equal-priority neighbour and choose 1.
	const n = 257
	g := Graph{Row: make([]uint32, n+1), Col: []uint32{256, 0}}
	for i := 1; i <= 256; i++ {
		g.Row[i] = 1
	}
	g.Row[n] = 2
	state, priority := initialState(n, 2, 42)
	for i := range priority {
		priority[i] = 7
	}
	s := testStorage{storage: mem.NewStorage(16 << 20), reverse: true}
	s.put(t, 0x100000, g.Row)
	s.put(t, 0x200000, g.Col)
	s.put(t, 0x400000, state)
	s.put(t, 0x500000, priority)
	s.launch(t, "graph_inplace", &GraphArgs{Row: 0x100000, Col: 0x200000, State: 0x400000, Priority: 0x500000, Counts: 0x600000, N: n, Mode: 2}, n)
	s.get(t, 0x400000, state, n*4)
	if state[0] != 1 || state[256] != 0 {
		t.Fatalf("same-round guard failed: %d %d", state[0], state[256])
	}
}

func TestSingleWriterPublishesEachImprovementInKernel(t *testing.T) {
	s := testStorage{storage: mem.NewStorage(16 << 20)}
	s.put(t, 0x100000, []uint32{0, 0, 2, 2})
	s.put(t, 0x200000, []uint32{0, 2})
	s.put(t, 0x300000, []uint32{5, 1})
	s.put(t, 0x400000, []uint32{0, infinity, 1})
	var writes []uint32
	s.onWrite = func(a uint64, b []byte) {
		if a == 0x400004 && len(b) == 4 {
			writes = append(writes, binary.LittleEndian.Uint32(b))
		}
	}
	s.launch(t, "graph_inplace", &GraphArgs{Row: 0x100000, Col: 0x200000, Weight: 0x300000, State: 0x400000, Counts: 0x600000, N: 3}, 3)
	if !reflect.DeepEqual(writes, []uint32{5, 2}) {
		t.Fatalf("not per-improvement publication: %v", writes)
	}
}

func TestCompiledGroupedEmbedding(t *testing.T) {
	const rows, dim, batches = 19, 8, 3
	for _, items := range []int{1, 17, 128, 256} {
		t.Run(fmt.Sprint(items), func(t *testing.T) {
			initial := make([]float32, rows*dim)
			for i := range initial {
				initial[i] = float32(i%23) / 32
			}
			ids := make([]uint32, items*batches)
			for i := range ids {
				ids[i] = uint32((i*7 + i/3) % rows)
			}
			s := testStorage{storage: mem.NewStorage(16 << 20)}
			s.put(t, 0x100000, initial)
			s.put(t, 0x200000, ids)
			for k := 0; k < batches; k++ {
				a := &EmbeddingArgs{Table: 0x100000, IDs: 0x200000, Output: 0x300000, Order: 0x400000, Dim: dim, LogDim: 3, Items: uint32(items), Offset: uint32(k * items), Step: 0.1 / float32(items)}
				s.launch(t, "embedding_lookup", a, items*dim)
				s.launch(t, "embedding_group", &GroupArgs{IDs: 0x200000, Order: 0x400000, Items: uint32(items), Offset: uint32(k * items)}, 256)
				order := make([]uint32, items)
				s.get(t, 0x400000, order, items*4)
				seen := map[uint32]bool{}
				for j, x := range order {
					if int(x) >= items || seen[x] {
						t.Fatal("invalid permutation", order)
					}
					seen[x] = true
					if j > 0 {
						prev := order[j-1]
						u, v := ids[k*items+int(prev)], ids[k*items+int(x)]
						if u > v || (u == v && prev > x) {
							t.Fatal("unstable grouping", order)
						}
					}
				}
				s.launch(t, "embedding_update", a, items*dim)
			}
			got := make([]float32, len(initial))
			s.get(t, 0x100000, got, len(got)*4)
			want := embeddingReference(initial, ids, dim, items, batches, 0.1)
			for i, x := range want {
				if math.Abs(float64(got[i]-x)) > 1e-6 || math.IsNaN(float64(got[i])) {
					t.Fatalf("element %d: %g want %g", i, got[i], x)
				}
			}
		})
	}
}
