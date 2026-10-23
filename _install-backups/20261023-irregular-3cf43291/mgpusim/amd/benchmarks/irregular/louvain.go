package irregular

import (
	"fmt"
	"math"
)

type LouvainLevel struct {
	Graph   Graph
	Labels  []uint32
	Quality float64
}

func modularity(g Graph, labels []uint32) float64 {
	total := float64(0)
	inside := float64(0)
	vol := map[uint32]float64{}
	for v := 0; v < len(g.Row)-1; v++ {
		for e := g.Row[v]; e < g.Row[v+1]; e++ {
			w := float64(g.W[e])
			total += w
			vol[labels[v]] += w
			if labels[v] == labels[g.Col[e]] {
				inside += w
			}
		}
	}
	if total == 0 {
		return 0
	}
	q := inside / total
	for _, v := range vol {
		q -= (v / total) * (v / total)
	}
	return q
}
func verifyLouvainLevel(level LouvainLevel, tolerance float64) error {
	g, labels := level.Graph, level.Labels
	n := len(g.Row) - 1
	degree, total := make([]float64, n), float64(0)
	vol := make([]float64, n)
	if len(labels) != n {
		return fmt.Errorf("Louvain label count")
	}
	for v := 0; v < n; v++ {
		if labels[v] >= uint32(n) {
			return fmt.Errorf("invalid community")
		}
		for e := g.Row[v]; e < g.Row[v+1]; e++ {
			degree[v] += float64(g.W[e])
		}
		total += degree[v]
		vol[labels[v]] += degree[v]
	}
	for v := 0; v < n; v++ {
		old := labels[v]
		weights := map[uint32]float64{}
		for e := g.Row[v]; e < g.Row[v+1]; e++ {
			if g.Col[e] != uint32(v) {
				weights[labels[g.Col[e]]] += float64(g.W[e])
			}
		}
		for c, w := range weights {
			if c == old {
				continue
			}
			gain := w - weights[old] + degree[v]*(vol[old]-degree[v]-vol[c])/total
			if gain > tolerance {
				return fmt.Errorf("Louvain level not locally converged: v=%d gain=%g", v, gain)
			}
		}
	}
	return nil
}
func contractReference(g Graph, labels, mapping []uint32, newN int) Graph {
	edges := make([][]struct {
		v uint32
		w float32
	}, newN)
	for v := 0; v < len(g.Row)-1; v++ {
		src := mapping[labels[v]]
		for e := g.Row[v]; e < g.Row[v+1]; e++ {
			edges[src] = append(edges[src], struct {
				v uint32
				w float32
			}{mapping[labels[g.Col[e]]], g.W[e]})
		}
	}
	// GPU preserves parallel edges instead of merging them. Both have identical
	// weighted degree and modularity. Verification compares these invariants.
	out := Graph{Row: make([]uint32, newN+1)}
	for v, list := range edges {
		for _, e := range list {
			out.Col = append(out.Col, e.v)
			out.W = append(out.W, e.w)
		}
		out.Row[v+1] = uint32(len(out.Col))
	}
	return out
}
func identity(n int) []uint32 {
	x := make([]uint32, n)
	for i := range x {
		x[i] = uint32(i)
	}
	return x
}
func nextPower(n int) int {
	p := 1
	for p < n {
		p *= 2
	}
	return p
}
func (b *Benchmark) runLouvain() {
	g, err := graph(b.Nodes, b.Degree, b.Components, b.GraphKind, b.Seed)
	if err != nil {
		panic(err)
	}
	original := g
	n0 := b.Nodes
	maxEdges := len(g.Col)
	if maxEdges == 0 {
		panic("Louvain needs at least one edge")
	}
	sortedSize := nextPower(maxEdges)
	a := Args{}
	a.A[0] = b.u32(g.Row)
	a.A[1] = b.u32(g.Col)
	a.A[2] = b.floats(g.W)
	for _, i := range []int{3, 4, 5, 6, 8, 9} {
		a.A[i] = b.alloc(n0 * 4)
	}
	a.A[7] = b.alloc((n0 + 255) / 256 * 4)
	a.A[10] = b.u32(identity(n0))
	a.A[11] = b.alloc(sortedSize * 16)
	levels := []LouvainLevel{}
	rounds := 0
	initialQ := modularity(original, identity(n0))
	lastQ := initialQ
	contractions := 0
	for level := 0; level < b.MaxRounds; level++ {
		n := len(g.Row) - 1
		a.P[0] = uint32(n)
		total := float32(0)
		for _, w := range g.W {
			total += w
		}
		a.F[0] = total
		// Gain tolerance is absolute weight, translated from a small modularity tolerance.
		a.F[1] = 1e-6
		b.launch("louvain_degree", n, a)
		b.launch("louvain_totals", n, a)
		done := false
		for iter := 0; iter < b.MaxRounds; iter++ {
			rounds++
			b.launch("louvain_propose", n, a)
			b.launch("louvain_select", n, a)
			if b.progress(a.A[7], n) == 0 {
				done = true
				break
			}
			b.launch("louvain_apply", n, a)
			b.launch("louvain_totals", n, a)
		}
		if !done {
			panic("Louvain move budget exceeded")
		}
		labels := b.read(a.A[3], n)
		quality := modularity(g, labels)
		if quality+1e-5 < lastQ {
			panic("Louvain modularity decreased")
		}
		levels = append(levels, LouvainLevel{g, labels, quality})
		lastQ = quality
		b.launch("louvain_compact", n, a)
		newN := b.progress(a.A[7], n)
		mapping := b.read(a.A[9], n)
		a.P[1] = uint32(n0)
		b.launch("louvain_membership", n0, a)
		if newN == n {
			break
		}
		if newN < 1 {
			panic("Louvain lost all communities")
		}
		// GPU rewrites all edge endpoints, sorts COO with a timed bitonic network,
		// then reconstructs CSR. Parallel edges and self-loops remain weighted.
		a.P[1] = uint32(maxEdges)
		a.P[2] = uint32(sortedSize)
		b.launch("louvain_edges", sortedSize, a)
		sorting := a
		sorting.P[0] = uint32(sortedSize)
		for width := 2; width <= sortedSize; width *= 2 {
			for stride := width / 2; stride > 0; stride /= 2 {
				sorting.P[1], sorting.P[2] = uint32(stride), uint32(width)
				b.launch("louvain_sort", sortedSize, sorting)
			}
		}
		a.P[0] = uint32(newN)
		a.P[1] = uint32(maxEdges)
		extent := maxEdges
		if newN+1 > extent {
			extent = newN + 1
		}
		b.launch("louvain_csr", extent, a)
		actual := Graph{b.read(a.A[0], newN+1), b.read(a.A[1], maxEdges), b.readFloat(a.A[2], maxEdges)}
		// Host validation is not used to build or transfer the contracted graph.
		expected := contractReference(g, labels, mapping, newN)
		if !equivalentWeightedGraphs(actual, expected) {
			panic("GPU Louvain contraction mismatch")
		}
		g = actual
		contractions++
		if level+1 == b.MaxRounds {
			panic("Louvain level budget exceeded")
		}
	}
	result := b.read(a.A[10], n0)
	finalQ := modularity(original, result)
	b.verify = func() error {
		if finalQ+1e-5 < initialQ || math.IsNaN(finalQ) {
			return fmt.Errorf("invalid final modularity")
		}
		for _, level := range levels {
			if err := verifyLouvainLevel(level, 2e-5); err != nil {
				return err
			}
		}
		if math.Abs(finalQ-lastQ) > 1e-4 {
			return fmt.Errorf("contraction membership changed modularity")
		}
		return nil
	}
	b.metadata["vertices"] = n0
	b.metadata["directed_edges"] = maxEdges
	b.metadata["rounds"] = rounds
	b.metadata["levels"] = len(levels)
	b.metadata["gpu_contractions"] = contractions
	b.metadata["initial_modularity"] = initialQ
	b.metadata["final_modularity"] = finalQ
	b.metadata["scope"] = "complete multilevel weighted Louvain; retains parallel edges and self loops during GPU contraction"
	b.metadata["update_semantics"] = "propose, disjoint-community arbitration, apply, totals; deterministic scheduling replaces upstream atomic community updates"
	b.metadata["arbitration_cost"] = "GPU scans lower vertex IDs; may be quadratic; all work is timed"
}
func equivalentWeightedGraphs(a, b Graph) bool {
	if len(a.Row) != len(b.Row) {
		return false
	}
	for v := 0; v < len(a.Row)-1; v++ {
		aa, bb := map[uint32]float64{}, map[uint32]float64{}
		for e := a.Row[v]; e < a.Row[v+1]; e++ {
			aa[a.Col[e]] += float64(a.W[e])
		}
		for e := b.Row[v]; e < b.Row[v+1]; e++ {
			bb[b.Col[e]] += float64(b.W[e])
		}
		if len(aa) != len(bb) {
			return false
		}
		for u, w := range aa {
			if math.Abs(w-bb[u]) > 1e-5 {
				return false
			}
		}
	}
	return true
}
