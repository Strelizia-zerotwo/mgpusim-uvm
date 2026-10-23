package irregular

func (b *Benchmark) runSCC() {
	g, err := directed(b.Nodes, b.Degree, b.Components, b.Seed)
	if err != nil {
		panic(err)
	}
	rev := reverse(g)
	n := b.Nodes
	state := make([]uint32, n)
	for i := range state {
		state[i] = none
	}
	a := Args{}
	a.P[0] = uint32(n)
	a.A[0] = b.u32(rev.Row)
	a.A[1] = b.u32(rev.Col)
	a.A[2] = b.u32(g.Row)
	a.A[3] = b.u32(g.Col)
	a.A[4] = b.u32(state)
	a.A[5] = b.alloc(n * 4)
	a.A[6] = b.alloc(n * 4)
	a.A[7] = b.alloc((n + 255) / 256 * 4)
	searches, rounds := 0, 0
	for {
		rounds += b.until("scc_trim", n, a, a.A[7])
		b.launch("scc_pivot", n, a)
		pivot := none
		for _, x := range b.read(a.A[7], (n+255)/256) {
			if x < pivot {
				pivot = x
			}
		}
		if pivot == none {
			break
		}
		searches++
		if searches > b.MaxRounds {
			panic("SCC pivot budget exceeded")
		}
		a.P[1] = pivot
		b.launch("scc_seed", n, a)
		rounds += b.until("scc_reach", n, a, a.A[7])
		backward := a
		backward.A[0], backward.A[1] = a.A[2], a.A[3]
		backward.A[5] = a.A[6]
		rounds += b.until("scc_reach", n, backward, a.A[7])
		b.launch("scc_assign", n, a)
	}
	result := b.read(a.A[4], n)
	b.verify = func() error { return samePartition(tarjan(g), result) }
	b.metadata["vertices"] = n
	b.metadata["directed_edges"] = len(g.Col)
	b.metadata["rounds"] = rounds
	b.metadata["pivot_searches"] = searches
	b.metadata["graph_kind"] = "directed-component-chain-random-relabelled"
	b.metadata["update_semantics"] = "in-place unique-vertex-writer reachability and trim; no proposal snapshot"
}
