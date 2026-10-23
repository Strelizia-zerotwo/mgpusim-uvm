package irregular

import (
	"fmt"
	"github.com/sarchlab/akita/v5/mem"
	"reflect"
	"testing"
)

func TestCompiledAlgorithmControllers(t *testing.T) {
	for _, algorithm := range []string{"SCC", "PPR", "SYMGS", "PTA", "LP", "LOUVAIN", "DMR"} {
		t.Run(algorithm, func(t *testing.T) {
			backend := &compiledBackend{t: t, s: testStorage{storage: mem.NewStorage(32 << 20)}, next: 0x100000}
			b := &Benchmark{backend: backend, Algorithm: algorithm, Nodes: 17, Degree: 4, Components: 2, MaxRounds: 256,
				Sweeps: 2, Objects: 4, Stores: 3, Points: 4, MaxInsertions: 16, MaxArea: 0.3, Alpha: 0.15, Epsilon: 0.01,
				GraphKind: "random", Seed: 42, metadata: map[string]interface{}{"kernel_launches": 0}}
			switch algorithm {
			case "SCC":
				b.runSCC()
			case "PPR":
				b.runPPR()
			case "SYMGS", "LP":
				b.runColored()
			case "PTA":
				b.runPTA()
			case "LOUVAIN":
				b.runLouvain()
			case "DMR":
				b.runMesh()
			}
			if err := b.verify(); err != nil {
				t.Fatal(err)
			}
			if b.metadata["kernel_launches"].(int) < 1 {
				t.Fatal("no compiled kernel executed")
			}
		})
	}
}
func TestMonotoneControllersAcrossWorkgroupOrders(t *testing.T) {
	for _, algorithm := range []string{"SCC", "PTA"} {
		for _, reversed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reversed=%v", algorithm, reversed), func(t *testing.T) {
				backend := &compiledBackend{t: t, s: testStorage{storage: mem.NewStorage(32 << 20), reverse: reversed}, next: 0x100000}
				b := &Benchmark{backend: backend, Algorithm: algorithm, Nodes: 257, Degree: 3, Components: 3, MaxRounds: 512,
					Objects: 33, Stores: 4, Seed: 43, metadata: map[string]interface{}{"kernel_launches": 0}}
				if algorithm == "SCC" {
					b.runSCC()
				} else {
					b.runPTA()
				}
				if err := b.verify(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
func TestAndersenAllConstraintKindsAndWordBoundary(t *testing.T) {
	// p4=&o32, p5=p4, p6=&o0, *p6=p5, p7=*p6.
	p := PTAInput{Nodes: 40, Objects: 33, Constraints: []Constraint{{0, 4, 32}, {1, 5, 4}, {0, 6, 0}, {3, 6, 5}, {2, 7, 6}}}
	result, err := ptaReference(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []int{0, 4, 5, 7} {
		if result[v*2+1]&1 == 0 {
			t.Fatalf("missing object32 at pointer%d", v)
		}
	}
}
func TestSCCReferenceDistinguishesOneWayReachability(t *testing.T) {
	g := fromAdj([]map[int]bool{{1: true}, {0: true, 2: true}, {3: true}, {2: true}, {}})
	got := tarjan(g)
	if err := samePartition([]uint32{0, 0, 2, 2, 4}, got); err != nil {
		t.Fatal(err)
	}
	if samePartition(got, []uint32{0, 0, 0, 0, 4}) == nil {
		t.Fatal("accepted merged SCCs")
	}
}
func TestColoringProtectsInPlaceUpdates(t *testing.T) {
	g, err := graph(81, 4, 1, "grid", 42)
	if err != nil {
		t.Fatal(err)
	}
	colors, _ := coloring(g)
	for v := 0; v < 81; v++ {
		for e := g.Row[v]; e < g.Row[v+1]; e++ {
			if colors[v] == colors[g.Col[e]] {
				t.Fatal("adjacent same color")
			}
		}
	}
}
func TestContractionPreservesModularityWithSelfLoopsAndDuplicateEdges(t *testing.T) {
	g := Graph{Row: []uint32{0, 2, 4, 6}, Col: []uint32{1, 1, 0, 2, 1, 0}, W: []float32{1, 2, 3, 4, 4, 0}}
	labels, mapping := []uint32{0, 0, 2}, []uint32{0, none, 1}
	out := contractReference(g, labels, mapping, 2)
	if !closeFloat(float32(modularity(g, labels)), float32(modularity(out, identity(2))), 1e-6) {
		t.Fatal("modularity changed")
	}
	if reflect.DeepEqual(out.Col, g.Col) {
		t.Fatal("edges not contracted")
	}
}
func TestGeometryVerifierRejectsHolesAndNonDelaunayMesh(t *testing.T) {
	points := meshPoints(4, 42)
	triangles := []Triangle{{3, 4, 5}, {3, 5, 6}}
	if err := verifyMesh(points, triangles, 0.6); err != nil {
		t.Fatal(err)
	}
	if verifyMesh(points, triangles[:1], 0.6) == nil {
		t.Fatal("accepted a hole")
	}
	if verifyMesh(points, triangles, 0.4) == nil {
		t.Fatal("accepted area above target")
	}
}

// Exercise float/vector and contraction paths across multiple workgroups,
// plus the documented DMR default geometry. This is instruction emulation,
// without a timing platform or replica protocol.
func TestCompiledMultiWorkgroupAndMeshGeometry(t *testing.T) {
	for _, algorithm := range []string{"PPR", "SYMGS", "LP", "LOUVAIN", "DMR"} {
		t.Run(algorithm, func(t *testing.T) {
			backend := &compiledBackend{t: t, s: testStorage{storage: mem.NewStorage(32 << 20), reverse: true}, next: 0x100000}
			b := &Benchmark{backend: backend, Algorithm: algorithm, Nodes: 257, Degree: 4, Components: 4, MaxRounds: 1024,
				Sweeps: 2, Objects: 4, Stores: 3, Points: 16, MaxInsertions: 128, MaxArea: 0.04, Alpha: 0.15, Epsilon: 0.001,
				GraphKind: "random", Seed: 42, metadata: map[string]interface{}{"kernel_launches": 0}}
			switch algorithm {
			case "PPR":
				b.runPPR()
			case "SYMGS", "LP":
				b.runColored()
			case "LOUVAIN":
				b.runLouvain()
			case "DMR":
				b.runMesh()
			}
			if err := b.verify(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
