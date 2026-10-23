package irregular

import "fmt"

func symgsReference(g Graph, colors []uint32, ncolors, sweeps int, rhs []float32) []float32 {
	x := make([]float32, len(colors))
	for sweep := 0; sweep < sweeps; sweep++ {
		for phase := 0; phase < 2*ncolors; phase++ {
			c := phase
			if c >= ncolors {
				c = 2*ncolors - 1 - c
			}
			for v := range x {
				if int(colors[v]) != c {
					continue
				}
				sum := rhs[v]
				for e := g.Row[v]; e < g.Row[v+1]; e++ {
					sum += x[g.Col[e]]
				}
				x[v] = sum / float32(g.Row[v+1]-g.Row[v]+1)
			}
		}
	}
	return x
}
func lpReference(g Graph, colors []uint32, ncolors, maxRounds int) ([]uint32, error) {
	state := make([]uint32, len(colors))
	for v := range state {
		state[v] = uint32(v)
	}
	for round := 0; round < maxRounds; round++ {
		changes := 0
		for c := 0; c < ncolors; c++ {
			for v := range state {
				if int(colors[v]) != c {
					continue
				}
				votes := map[uint32]int{}
				for e := g.Row[v]; e < g.Row[v+1]; e++ {
					votes[state[g.Col[e]]]++
				}
				old, best := state[v], state[v]
				bestN := 0
				for e := g.Row[v]; e < g.Row[v+1]; e++ {
					label := state[g.Col[e]]
					if votes[label] > bestN || (votes[label] == bestN && label == old) {
						best, bestN = label, votes[label]
					}
				}
				if bestN > votes[old] {
					state[v] = best
					changes++
				}
			}
		}
		if changes == 0 {
			return state, nil
		}
	}
	return nil, fmt.Errorf("LP reference did not converge")
}
func (b *Benchmark) runColored() {
	g, err := graph(b.Nodes, b.Degree, 1, b.GraphKind, b.Seed)
	if err != nil {
		panic(err)
	}
	n := b.Nodes
	colors, ncolors := coloring(g)
	a := Args{}
	a.P[0] = uint32(n)
	a.A[0] = b.u32(g.Row)
	a.A[1] = b.u32(g.Col)
	a.A[2] = b.u32(colors)
	a.A[7] = b.alloc((n + 255) / 256 * 4)
	rounds := 0
	if b.Algorithm == "SYMGS" {
		if b.Sweeps < 1 {
			panic("sweeps must be positive")
		}
		rhs := make([]float32, n)
		for i := range rhs {
			rhs[i] = 1 + float32(i%7)/16
		}
		a.A[3] = b.floats(make([]float32, n))
		a.A[4] = b.floats(rhs)
		for sweep := 0; sweep < b.Sweeps; sweep++ {
			for phase := 0; phase < 2*ncolors; phase++ {
				c := phase
				if c >= ncolors {
					c = 2*ncolors - 1 - c
				}
				a.P[1] = uint32(c)
				b.launch("symgs", n, a)
			}
		}
		result := b.readFloat(a.A[3], n)
		b.verify = func() error {
			want := symgsReference(g, colors, ncolors, b.Sweeps, rhs)
			for v, x := range result {
				if !closeFloat(want[v], x, 2e-5) {
					return fmt.Errorf("SymGS mismatch at %d", v)
				}
			}
			return nil
		}
		rounds = b.Sweeps
		b.metadata["completion_criterion"] = "requested complete forward/backward sweeps, not linear-solver convergence"
	} else {
		initial := make([]uint32, n)
		for v := range initial {
			initial[v] = uint32(v)
		}
		a.A[3] = b.u32(initial)
		for rounds < b.MaxRounds {
			rounds++
			changes := 0
			for c := 0; c < ncolors; c++ {
				a.P[1] = uint32(c)
				b.launch("label_propagation", n, a)
				changes += b.progress(a.A[7], n)
			}
			if changes == 0 {
				break
			}
			if rounds == b.MaxRounds {
				panic("LP convergence budget exceeded")
			}
		}
		result := b.read(a.A[3], n)
		b.verify = func() error {
			want, err := lpReference(g, colors, ncolors, b.MaxRounds)
			if err != nil {
				return err
			}
			for v, x := range result {
				if x != want[v] {
					return fmt.Errorf("LP mismatch at %d", v)
				}
			}
			return nil
		}
		b.metadata["update_semantics"] = "colored in-place majority label propagation; strict improvement, retain old label on ties; different schedule from Gunrock LP"
	}
	b.metadata["scope"] = "colored in-place majority label propagation"
	if b.Algorithm == "SYMGS" {
		b.metadata["scope"] = "colored symmetric Gauss-Seidel, diagonally dominant generated matrix"
		b.metadata["update_semantics"] = "in-place x reads/writes by proper graph color; no old-state snapshot"
	}
	b.metadata["vertices"] = n
	b.metadata["directed_edges"] = len(g.Col)
	b.metadata["rounds"] = rounds
	b.metadata["colors"] = ncolors
	b.metadata["graph_kind"] = b.GraphKind
}
