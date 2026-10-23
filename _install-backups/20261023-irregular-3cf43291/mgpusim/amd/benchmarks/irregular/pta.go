package irregular

import (
	"fmt"
	"math/rand"
)

// Object IDs are also pointer nodes: dereferencing object o accesses pts[o].
type Constraint struct{ Kind, Left, Right uint32 } // 0 address,1 copy,2 load,3 store
type PTAInput struct {
	Nodes, Objects int
	Constraints    []Constraint
}

func ptaInput(n, objects, degree, stores int, seed int64) (PTAInput, error) {
	if objects < 1 || objects > n || degree < 1 || stores < 0 {
		return PTAInput{}, fmt.Errorf("require 1<=objects<=nodes, degree>=1 and stores>=0")
	}
	rng := rand.New(rand.NewSource(seed))
	p := PTAInput{Nodes: n, Objects: objects}
	// Heap object fields and a subset of variables start with address facts.
	for o := 0; o < objects; o++ {
		p.Constraints = append(p.Constraints, Constraint{0, uint32(o), uint32((o + 1) % objects)})
	}
	for v := objects; v < n; v++ {
		if v%8 == 0 {
			p.Constraints = append(p.Constraints, Constraint{0, uint32(v), uint32(rng.Intn(objects))})
		}
		for j := 0; j < degree; j++ {
			src := rng.Intn(n)
			kind := uint32(1)
			if j == 0 && v%8 == 1 {
				kind = 2
			}
			p.Constraints = append(p.Constraints, Constraint{kind, uint32(v), uint32(src)})
		}
	}
	for i := 0; i < stores; i++ {
		p.Constraints = append(p.Constraints, Constraint{3, uint32(rng.Intn(n)), uint32(rng.Intn(n))})
	}
	return p, nil
}

// This verifier applies the four declarative constraints directly. It does not
// reproduce the GPU's destination-indexed gather schedule.
func ptaReference(p PTAInput) ([]uint32, error) {
	words := (p.Objects + 31) / 32
	sets := make([]uint32, p.Nodes*words)
	for _, c := range p.Constraints {
		if int(c.Left) >= p.Nodes || int(c.Right) >= p.Nodes || c.Kind > 3 {
			return nil, fmt.Errorf("invalid constraint")
		}
		if c.Kind == 0 {
			if int(c.Right) >= p.Objects {
				return nil, fmt.Errorf("invalid address fact")
			}
			sets[int(c.Left)*words+int(c.Right)/32] |= 1 << (c.Right % 32)
		}
	}
	union := func(dst, src uint32) bool {
		changed := false
		for w := 0; w < words; w++ {
			i, j := int(dst)*words+w, int(src)*words+w
			old := sets[i]
			sets[i] |= sets[j]
			changed = changed || old != sets[i]
		}
		return changed
	}
	// Every successful update adds at least one of Nodes*Objects finite facts.
	for round := 0; round <= p.Nodes*p.Objects; round++ {
		changed := false
		for _, c := range p.Constraints {
			switch c.Kind {
			case 1:
				changed = union(c.Left, c.Right) || changed
			case 2:
				for o := 0; o < p.Objects; o++ {
					if sets[int(c.Right)*words+o/32]&(1<<uint(o%32)) != 0 {
						changed = union(c.Left, uint32(o)) || changed
					}
				}
			case 3:
				for o := 0; o < p.Objects; o++ {
					if sets[int(c.Left)*words+o/32]&(1<<uint(o%32)) != 0 {
						changed = union(uint32(o), c.Right) || changed
					}
				}
			}
		}
		if !changed {
			return sets, nil
		}
	}
	return nil, fmt.Errorf("PTA finite closure did not terminate")
}
func (b *Benchmark) runPTA() {
	p, err := ptaInput(b.Nodes, b.Objects, b.Degree, b.Stores, b.Seed)
	if err != nil {
		panic(err)
	}
	n, words := p.Nodes, (p.Objects+31)/32
	initial := make([]uint32, n*words)
	incoming := make([][]Constraint, n)
	stores := []uint32{}
	kindCounts := [4]int{}
	for _, c := range p.Constraints {
		kindCounts[c.Kind]++
		switch c.Kind {
		case 0:
			initial[int(c.Left)*words+int(c.Right)/32] |= 1 << (c.Right % 32)
		case 1, 2:
			incoming[c.Left] = append(incoming[c.Left], c)
		case 3:
			stores = append(stores, c.Left, c.Right)
		}
	}
	row, src, kind := make([]uint32, n+1), []uint32{}, []uint32{}
	for v, list := range incoming {
		for _, c := range list {
			src = append(src, c.Right)
			kind = append(kind, c.Kind-1)
		}
		row[v+1] = uint32(len(src))
	}
	a := Args{}
	a.P = [8]uint32{uint32(n), uint32(p.Objects), uint32(words), uint32(len(stores) / 2)}
	a.A[0] = b.u32(row)
	a.A[1] = b.u32(src)
	a.A[2] = b.u32(kind)
	a.A[3] = b.u32(stores)
	a.A[4] = b.u32(initial)
	a.A[7] = b.alloc((n + 255) / 256 * 4)
	rounds := b.until("points_to", n, a, a.A[7])
	result := b.read(a.A[4], len(initial))
	b.verify = func() error {
		want, err := ptaReference(p)
		if err != nil {
			return err
		}
		for i, x := range result {
			if x != want[i] {
				return fmt.Errorf("PTA closure mismatch at word %d", i)
			}
		}
		return nil
	}
	b.metadata["vertices"] = n
	b.metadata["objects"] = p.Objects
	b.metadata["constraints_by_kind"] = kindCounts
	b.metadata["rounds"] = rounds
	b.metadata["scope"] = "finite flow/context-insensitive Andersen closure: address,copy,load,store; no casts/field sensitivity"
	b.metadata["update_semantics"] = "monotone in-place bitsets, one pointer-row writer; store constraints scanned per object row; no proposal snapshot"
	b.metadata["source_fidelity"] = "algorithmic implementation; replaces upstream sparse dynamic sets/HCD with bounded bitsets, not the original Lonestar memory trace"
}
