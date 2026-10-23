package cudaports2

import (
	"container/heap"
	"fmt"
	"math"
	"math/rand"
	"sort"
)

type Graph struct{ Row, Col, Weight []uint32 }

// GenerateGraph creates undirected positive-weight CSR input. Random inputs
// include a chain inside each requested component; no edge joins components.
// Grid inputs use four-neighbour adjacency. Neither is a claimed real dataset.
func GenerateGraph(n, degree, components int, kind string, seed int64) (Graph, error) {
	if n < 1 || degree < 2 || components < 1 || components > n {
		return Graph{}, fmt.Errorf("invalid node, degree or component count")
	}
	if kind != "random" && kind != "grid" {
		return Graph{}, fmt.Errorf("graph must be random or grid")
	}
	adj := make([]map[int]bool, n)
	for i := range adj {
		adj[i] = map[int]bool{}
	}
	add := func(a, b int) {
		if a != b {
			adj[a][b] = true
			adj[b][a] = true
		}
	}
	rng := rand.New(rand.NewSource(seed))
	if kind == "grid" {
		side := int(math.Sqrt(float64(n)))
		if side*side != n || components != 1 {
			return Graph{}, fmt.Errorf("grid requires a square node count and components=1")
		}
		for v := 0; v < n; v++ {
			if v%side+1 < side {
				add(v, v+1)
			}
			if v+side < n {
				add(v, v+side)
			}
		}
	} else {
		for component := 0; component < components; component++ {
			lo, hi := n*component/components, n*(component+1)/components
			for v := lo; v < hi; v++ {
				if v+1 < hi {
					add(v, v+1)
				}
				for j := 0; j < degree/2-1; j++ {
					add(v, lo+rng.Intn(hi-lo))
				}
			}
		}
	}
	if kind == "random" {
		// Avoid making component IDs coincide with the four contiguous GPU
		// workgroup/home partitions. Relabelling preserves graph connectivity.
		permutation := rand.New(rand.NewSource(seed + 43)).Perm(n)
		relabelled := make([]map[int]bool, n)
		for i := range relabelled {
			relabelled[i] = map[int]bool{}
		}
		for v, neighbors := range adj {
			for u := range neighbors {
				relabelled[permutation[v]][permutation[u]] = true
			}
		}
		adj = relabelled
	}
	g := Graph{Row: make([]uint32, n+1)}
	for v, neighbors := range adj {
		keys := make([]int, 0, len(neighbors))
		for u := range neighbors {
			keys = append(keys, u)
		}
		sort.Ints(keys)
		for _, u := range keys {
			g.Col = append(g.Col, uint32(u))
			a, b := v, u
			if a > b {
				a, b = b, a
			}
			g.Weight = append(g.Weight, 1+uint32((uint64(a)*1103515245+uint64(b)*12345+uint64(seed))%16))
		}
		g.Row[v+1] = uint32(len(g.Col))
	}
	// A one-vertex graph still needs a non-empty allocation for the edge arrays.
	return g, nil
}

type distance struct {
	vertex int
	value  uint32
}
type distanceHeap []distance

func (h distanceHeap) Len() int            { return len(h) }
func (h distanceHeap) Less(i, j int) bool  { return h[i].value < h[j].value }
func (h distanceHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *distanceHeap) Push(x interface{}) { *h = append(*h, x.(distance)) }
func (h *distanceHeap) Pop() interface{}   { x := (*h)[len(*h)-1]; *h = (*h)[:len(*h)-1]; return x }

func shortestPaths(g Graph) []uint32 {
	n := len(g.Row) - 1
	d := make([]uint32, n)
	for i := range d {
		d[i] = infinity
	}
	d[0] = 0
	q := &distanceHeap{{vertex: 0, value: 0}}
	heap.Init(q)
	for q.Len() > 0 {
		x := heap.Pop(q).(distance)
		if x.value != d[x.vertex] {
			continue
		}
		for e := g.Row[x.vertex]; e < g.Row[x.vertex+1]; e++ {
			u := g.Col[e]
			next := x.value + g.Weight[e]
			if next < d[u] {
				d[u] = next
				heap.Push(q, distance{int(u), next})
			}
		}
	}
	return d
}
func componentsReference(g Graph) []uint32 {
	n := len(g.Row) - 1
	labels := make([]uint32, n)
	for i := range labels {
		labels[i] = infinity
	}
	for root := 0; root < n; root++ {
		if labels[root] != infinity {
			continue
		}
		labels[root] = uint32(root)
		queue := []int{root}
		for head := 0; head < len(queue); head++ {
			v := queue[head]
			for e := g.Row[v]; e < g.Row[v+1]; e++ {
				u := g.Col[e]
				if labels[u] == infinity {
					labels[u] = uint32(root)
					queue = append(queue, int(u))
				}
			}
		}
	}
	return labels
}
func verifyGraph(g Graph, algorithm string, state []uint32) error {
	n := len(g.Row) - 1
	if len(state) != n {
		return fmt.Errorf("%s output has %d vertices, expected %d", algorithm, len(state), n)
	}
	var want []uint32
	if algorithm == "SSSP" {
		want = shortestPaths(g)
	} else if algorithm == "CC" {
		want = componentsReference(g)
	}
	for v, x := range state {
		if want != nil {
			if x != want[v] {
				return fmt.Errorf("%s vertex %d: got %d want %d", algorithm, v, x, want[v])
			}
			continue
		}
		if algorithm == "COLOR" && x == infinity {
			return fmt.Errorf("uncoloured vertex %d", v)
		}
		if algorithm == "MIS" && x != 1 && x != 2 {
			return fmt.Errorf("undecided MIS vertex %d", v)
		}
		markedNeighbor := false
		for e := g.Row[v]; e < g.Row[v+1]; e++ {
			u := g.Col[e]
			if algorithm == "COLOR" && x == state[u] {
				return fmt.Errorf("colour conflict on edge %d-%d", v, u)
			}
			if algorithm == "MIS" && state[u] == 1 {
				markedNeighbor = true
				if x == 1 {
					return fmt.Errorf("MIS independence violation on edge %d-%d", v, u)
				}
			}
		}
		if algorithm == "MIS" && x == 2 && !markedNeighbor {
			return fmt.Errorf("MIS maximality violation at %d", v)
		}
	}
	return nil
}
