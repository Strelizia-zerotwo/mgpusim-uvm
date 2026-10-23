package irregular

import (
	"fmt"
	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/cudaports2"
	"math/rand"
	"sort"
)

type Graph struct {
	Row, Col []uint32
	W        []float32
}

func graph(n, degree, components int, kind string, seed int64) (Graph, error) {
	g, err := cudaports2.GenerateGraph(n, degree, components, kind, seed)
	if err != nil {
		return Graph{}, err
	}
	weights := make([]float32, len(g.Col))
	for i := range weights {
		weights[i] = 1
	}
	return Graph{g.Row, g.Col, weights}, nil
}
func directed(n, degree, components int, seed int64) (Graph, error) {
	if n < components || components < 1 || degree < 2 {
		return Graph{}, fmt.Errorf("invalid directed graph dimensions")
	}
	rng := rand.New(rand.NewSource(seed))
	perm := rng.Perm(n)
	adj := make([]map[int]bool, n)
	for i := range adj {
		adj[i] = map[int]bool{}
	}
	for c := 0; c < components; c++ {
		lo, hi := n*c/components, n*(c+1)/components
		for v := lo; v < hi; v++ {
			adj[perm[v]][perm[lo+(v-lo+1)%(hi-lo)]] = true
			for j := 0; j < degree-1; j++ {
				u := lo + rng.Intn(hi-lo)
				if u != v {
					adj[perm[v]][perm[u]] = true
				}
			}
			if c+1 < components && v == lo {
				adj[perm[v]][perm[hi]] = true
			}
		}
	}
	return fromAdj(adj), nil
}
func fromAdj(adj []map[int]bool) Graph {
	g := Graph{Row: make([]uint32, len(adj)+1)}
	for v, m := range adj {
		keys := make([]int, 0, len(m))
		for u := range m {
			keys = append(keys, u)
		}
		sort.Ints(keys)
		for _, u := range keys {
			g.Col = append(g.Col, uint32(u))
			g.W = append(g.W, 1)
		}
		g.Row[v+1] = uint32(len(g.Col))
	}
	return g
}
func reverse(g Graph) Graph {
	n := len(g.Row) - 1
	adj := make([]map[int]bool, n)
	for i := range adj {
		adj[i] = map[int]bool{}
	}
	for v := 0; v < n; v++ {
		for e := g.Row[v]; e < g.Row[v+1]; e++ {
			adj[g.Col[e]][v] = true
		}
	}
	return fromAdj(adj)
}
func coloring(g Graph) ([]uint32, int) {
	n := len(g.Row) - 1
	c := make([]uint32, n)
	for i := range c {
		c[i] = none
	}
	used := 0
	for v := 0; v < n; v++ {
		seen := map[uint32]bool{}
		for e := g.Row[v]; e < g.Row[v+1]; e++ {
			seen[c[g.Col[e]]] = true
		}
		x := uint32(0)
		for seen[x] {
			x++
		}
		c[v] = x
		if int(x)+1 > used {
			used = int(x) + 1
		}
	}
	return c, used
}

// Independent Tarjan reference; GPU uses repeated forward/backward searches.
func tarjan(g Graph) []uint32 {
	n := len(g.Row) - 1
	index := make([]int, n)
	low := make([]int, n)
	on := make([]bool, n)
	labels := make([]uint32, n)
	for i := range index {
		index[i] = -1
	}
	stack := []int{}
	next := 0
	var visit func(int)
	visit = func(v int) {
		index[v] = next
		low[v] = next
		next++
		stack = append(stack, v)
		on[v] = true
		for e := g.Row[v]; e < g.Row[v+1]; e++ {
			u := int(g.Col[e])
			if index[u] < 0 {
				visit(u)
				if low[u] < low[v] {
					low[v] = low[u]
				}
			} else if on[u] && index[u] < low[v] {
				low[v] = index[u]
			}
		}
		if low[v] == index[v] {
			for {
				u := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				on[u] = false
				labels[u] = uint32(v)
				if u == v {
					break
				}
			}
		}
	}
	for v := 0; v < n; v++ {
		if index[v] < 0 {
			visit(v)
		}
	}
	return labels
}
func samePartition(want, got []uint32) error {
	if len(want) != len(got) {
		return fmt.Errorf("partition length mismatch")
	}
	forward, back := map[uint32]uint32{}, map[uint32]uint32{}
	for v, x := range got {
		if x == none {
			return fmt.Errorf("unassigned vertex %d", v)
		}
		w := want[v]
		if y, ok := forward[w]; ok && y != x {
			return fmt.Errorf("reference component split at %d", v)
		}
		if y, ok := back[x]; ok && y != w {
			return fmt.Errorf("different components merged at %d", v)
		}
		forward[w] = x
		back[x] = w
	}
	return nil
}
