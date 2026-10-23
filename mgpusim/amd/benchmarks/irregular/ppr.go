package irregular

import "fmt"

func pprReference(g Graph, alpha, eps float32, maxRounds int) ([]float32, []float32, int, error) {
	n := len(g.Row) - 1
	p, r, rp := make([]float32, n), make([]float32, n), make([]float32, n)
	active := make([]bool, n)
	r[0] = 1
	rp[0] = 1
	active[0] = true
	in := reverse(g)
	a, c := 2*alpha/(1+alpha), (1-alpha)/(1+alpha)
	for round := 1; round <= maxRounds; round++ {
		for v := 0; v < n; v++ {
			if active[v] {
				p[v] += a * r[v]
				rp[v] = 0
			}
		}
		next := make([]bool, n)
		total := 0
		for v := 0; v < n; v++ {
			for e := in.Row[v]; e < in.Row[v+1]; e++ {
				u := in.Col[e]
				if active[u] {
					rp[v] += c * r[u] / float32(g.Row[u+1]-g.Row[u])
				}
			}
			next[v] = rp[v] >= float32(g.Row[v+1]-g.Row[v])*eps
			if next[v] {
				total++
			}
		}
		copy(r, rp)
		active = next
		if total == 0 {
			return p, r, round, nil
		}
	}
	return nil, nil, 0, fmt.Errorf("PPR reference did not converge")
}
func (b *Benchmark) runPPR() {
	if !(b.Alpha > 0 && b.Alpha < 1 && b.Epsilon > 0) {
		panic("PPR requires 0<alpha<1 and epsilon>0")
	}
	g, err := graph(b.Nodes, b.Degree, 1, b.GraphKind, b.Seed)
	if err != nil {
		panic(err)
	}
	n := b.Nodes
	in := reverse(g)
	degree := make([]uint32, n)
	for v := range degree {
		degree[v] = g.Row[v+1] - g.Row[v]
		if degree[v] == 0 {
			panic("PPR input contains a sink")
		}
	}
	residual := make([]float32, n)
	residual[0] = 1
	active := make([]uint32, n)
	active[0] = 1
	a := Args{}
	a.P[0] = uint32(n)
	a.F = [4]float32{2 * b.Alpha / (1 + b.Alpha), (1 - b.Alpha) / (1 + b.Alpha), b.Epsilon, 0}
	a.A[0] = b.u32(in.Row)
	a.A[1] = b.u32(in.Col)
	a.A[2] = b.u32(degree)
	a.A[3] = b.floats(make([]float32, n))
	a.A[4] = b.floats(residual)
	a.A[5] = b.floats(residual)
	a.A[6] = b.u32(active)
	a.A[7] = b.alloc(n * 4)
	a.A[8] = b.alloc((n + 255) / 256 * 4)
	rounds := 0
	for rounds < b.MaxRounds {
		rounds++
		b.launch("ppr_filter", n, a)
		b.launch("ppr_advance", n, a)
		b.launch("ppr_copy", n, a)
		if b.progress(a.A[8], n) == 0 {
			break
		}
	}
	if b.progress(a.A[8], n) != 0 {
		panic("PPR convergence budget exceeded")
	}
	result, rest := b.readFloat(a.A[3], n), b.readFloat(a.A[4], n)
	b.verify = func() error {
		want, wr, _, err := pprReference(g, b.Alpha, b.Epsilon, b.MaxRounds)
		if err != nil {
			return err
		}
		mass := float64(0)
		for v, x := range result {
			if !closeFloat(want[v], x, 2e-5) || !closeFloat(wr[v], rest[v], 2e-5) {
				return fmt.Errorf("PPR mismatch at %d", v)
			}
			if x < 0 || rest[v] < 0 {
				return fmt.Errorf("negative PPR mass")
			}
			mass += float64(x + rest[v])
		}
		if mass < 0.999 || mass > 1.001 {
			return fmt.Errorf("PPR mass not conserved: %g", mass)
		}
		return nil
	}
	b.metadata["scope"] = "single-seed residual PPR with source filter/advance/dense-copy phases"
	b.metadata["vertices"] = n
	b.metadata["directed_edges"] = len(g.Col)
	b.metadata["rounds"] = rounds
	b.metadata["graph_kind"] = b.GraphKind
	b.metadata["alpha"] = b.Alpha
	b.metadata["epsilon"] = b.Epsilon
	b.metadata["update_semantics"] = "source filter/advance/dense-copy phases; incoming-edge destination writer replaces atomicAdd"
}
