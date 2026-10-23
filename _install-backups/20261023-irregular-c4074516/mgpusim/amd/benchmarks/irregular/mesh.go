package irregular

import (
	"fmt"
	"github.com/sarchlab/mgpusim/v5/amd/driver"
	"math"
	"math/rand"
)

type Point struct{ X, Y float32 }
type Triangle struct{ A, B, C uint32 }

func orientation(a, b, c Point) float64 {
	return float64(b.X-a.X)*float64(c.Y-a.Y) - float64(b.Y-a.Y)*float64(c.X-a.X)
}
func incircle(a, b, c, p Point) float64 {
	ax, ay := float64(a.X)-float64(p.X), float64(a.Y)-float64(p.Y)
	bx, by := float64(b.X)-float64(p.X), float64(b.Y)-float64(p.Y)
	cx, cy := float64(c.X)-float64(p.X), float64(c.Y)-float64(p.Y)
	return (ax*ax+ay*ay)*(bx*cy-by*cx) - (bx*bx+by*by)*(ax*cy-ay*cx) + (cx*cx+cy*cy)*(ax*by-ay*bx)
}
func meshPoints(n int, seed int64) []Point {
	if n < 4 {
		panic("DMR points must include at least four square corners")
	}
	points := []Point{{-16, -16}, {16, -16}, {0, 16}, {0, 0}, {1, 0}, {1, 1}, {0, 1}}
	rng := rand.New(rand.NewSource(seed))
	for i := 4; i < n; i++ {
		points = append(points, Point{0.01 + 0.98*rng.Float32(), 0.01 + 0.98*rng.Float32()})
	}
	return points
}

// Independent geometric checks: positive orientation, complete area coverage,
// edge manifoldness, use of every inserted point, and the empty-circle property.
func verifyMesh(points []Point, tris []Triangle, maxArea float64) error {
	if len(tris) == 0 {
		return fmt.Errorf("empty final mesh")
	}
	edges := map[[2]uint32]int{}
	used := map[uint32]bool{}
	area := float64(0)
	for _, tr := range tris {
		ids := [3]uint32{tr.A, tr.B, tr.C}
		for _, v := range ids {
			if v < 3 || int(v) >= len(points) {
				return fmt.Errorf("invalid final vertex %d", v)
			}
			used[v] = true
		}
		a, b, c := points[tr.A], points[tr.B], points[tr.C]
		signed := orientation(a, b, c)
		if signed <= 0 {
			return fmt.Errorf("nonpositive triangle orientation")
		}
		area += signed / 2
		if signed/2 > maxArea+2e-5 {
			return fmt.Errorf("unrefined triangle area %g", signed/2)
		}
		for i := 0; i < 3; i++ {
			x, y := ids[i], ids[(i+1)%3]
			if x > y {
				x, y = y, x
			}
			edges[[2]uint32{x, y}]++
		}
		for v := 3; v < len(points); v++ {
			if uint32(v) == tr.A || uint32(v) == tr.B || uint32(v) == tr.C {
				continue
			}
			if incircle(a, b, c, points[v]) > 2e-5 {
				return fmt.Errorf("Delaunay empty-circle violation at vertex %d", v)
			}
		}
	}
	if math.Abs(area-1) > 2e-4 {
		return fmt.Errorf("unit-square coverage %g", area)
	}
	for e, count := range edges {
		if count < 1 || count > 2 {
			return fmt.Errorf("nonmanifold edge")
		}
		if count == 1 {
			a, b := points[e[0]], points[e[1]]
			boundary := (a.X == 0 && b.X == 0) || (a.X == 1 && b.X == 1) || (a.Y == 0 && b.Y == 0) || (a.Y == 1 && b.Y == 1)
			if !boundary {
				return fmt.Errorf("hole in triangulation at edge %v", e)
			}
		}
	}
	for v := 3; v < len(points); v++ {
		if !used[uint32(v)] {
			return fmt.Errorf("inserted point %d lost", v)
		}
	}
	return nil
}
func prefix(counts []uint32) ([]uint32, int) {
	offsets := make([]uint32, len(counts))
	sum := uint32(0)
	for i, x := range counts {
		offsets[i] = sum
		sum += x
	}
	return offsets, int(sum)
}
func (b *Benchmark) meshRank(a Args, flags driver.Ptr, extent int, output driver.Ptr) int {
	scan := a
	scan.P[0] = uint32(extent)
	scan.A[3] = flags
	scan.A[8] = output
	b.launch("mesh_count", extent, scan)
	offsets, total := prefix(b.read(scan.A[6], (extent+255)/256))
	b.write(scan.A[7], offsets)
	b.launch("mesh_rank", extent, scan)
	return total
}
func (b *Benchmark) meshInsert(a Args, capacity, point int) {
	a.P[0], a.P[1] = uint32(capacity), uint32(point)
	b.launch("mesh_cavity", capacity, a)
	b.launch("mesh_boundary", 3*capacity, a)
	free := b.meshRank(a, a.A[3], capacity, a.A[9])
	boundary := b.meshRank(a, a.A[4], 3*capacity, a.A[8])
	if boundary < 3 {
		panic("empty or degenerate GPU insertion cavity")
	}
	if boundary > free {
		panic("DMR triangle capacity exhausted")
	}
	b.launch("mesh_clear", capacity, a)
	a.P[1], a.P[2] = uint32(boundary), uint32(point)
	b.launch("mesh_rebuild", boundary, a)
}
func (b *Benchmark) runMesh() {
	if b.Points < 4 || b.MaxInsertions < 0 || !(b.MaxArea > 0) || math.IsInf(float64(b.MaxArea), 0) {
		panic("invalid DMR points, max-insertions or max-area")
	}
	initial := meshPoints(b.Points, b.Seed)
	pointCapacity := len(initial) + b.MaxInsertions
	capacity := nextPower(2*pointCapacity + 16)
	if capacity < 1024 {
		capacity = 1024
	}
	xy := make([]float32, 2*pointCapacity)
	for i, p := range initial {
		xy[2*i], xy[2*i+1] = p.X, p.Y
	}
	tris := make([]uint32, 4*capacity)
	tris[0], tris[1], tris[2], tris[3] = 0, 1, 2, 1
	a := Args{}
	a.P[0] = uint32(capacity)
	a.A[0] = b.u32(tris)
	a.A[1] = b.floats(xy)
	a.A[2] = b.alloc(capacity * 4)
	a.A[3] = b.alloc(capacity * 4)
	a.A[4] = b.alloc(3 * capacity * 4)
	a.A[5] = b.alloc(6 * capacity * 4)
	a.A[6] = b.alloc((3*capacity + 255) / 256 * 4)
	a.A[7] = b.alloc((3*capacity + 255) / 256 * 4)
	a.A[8] = b.alloc(3 * capacity * 4)
	a.A[9] = b.alloc(capacity * 4)
	for p := 3; p < len(initial); p++ {
		b.meshInsert(a, capacity, p)
	}
	inserted := 0
	// Largest-area triangle selection is reduced on GPUs; only per-group
	// candidates cross to the host. Its centroid is computed on the GPU.
	for {
		selection := a
		selection.F[0] = b.MaxArea
		b.launch("mesh_select", capacity, selection)
		ids := b.read(a.A[6], (capacity+255)/256)
		areas := b.readFloat(a.A[7], len(ids))
		best, bestArea := none, float32(0)
		for i, id := range ids {
			if id != none && (areas[i] > bestArea || (areas[i] == bestArea && id < best)) {
				best, bestArea = id, areas[i]
			}
		}
		if best == none {
			break
		}
		if inserted == b.MaxInsertions {
			panic("DMR insertion budget reached before area criterion")
		}
		p := len(initial) + inserted
		centroid := a
		centroid.P[1], centroid.P[2] = best, uint32(p)
		b.launch("mesh_centroid", capacity, centroid)
		b.meshInsert(a, capacity, p)
		inserted++
	}
	raw, coords := b.read(a.A[0], 4*capacity), b.readFloat(a.A[1], 2*(len(initial)+inserted))
	points := make([]Point, len(coords)/2)
	for i := range points {
		points[i] = Point{coords[2*i], coords[2*i+1]}
	}
	final := []Triangle{}
	for i := 0; i < capacity; i++ {
		if raw[4*i+3] != 0 && raw[4*i] >= 3 && raw[4*i+1] >= 3 && raw[4*i+2] >= 3 {
			final = append(final, Triangle{raw[4*i], raw[4*i+1], raw[4*i+2]})
		}
	}
	b.verify = func() error { return verifyMesh(points, final, float64(b.MaxArea)) }
	b.metadata["vertices"] = len(points) - 3
	b.metadata["triangles"] = len(final)
	b.metadata["triangle_capacity"] = capacity
	b.metadata["initial_points"] = b.Points
	b.metadata["refinement_insertions"] = inserted
	b.metadata["rounds"] = b.Points + inserted
	b.metadata["max_area"] = b.MaxArea
	b.metadata["scope"] = "complete 2D Delaunay area refinement on a unit square: incremental Bowyer-Watson plus centroid insertion; NOT Lonestar Chew minimum-angle refinement"
	b.metadata["update_semantics"] = "GPU cavity, boundary, rank, clear, in-place slot rebuild; serial insertion batches; no host cavity/topology construction"
	b.metadata["prefix_controller"] = "GPU group counts and scans; host exclusive scan of group counts with timed D2H/H2D transfers"
	b.metadata["geometry_precision"] = "FP32 GPU predicates, FP64 host verification; fail rather than accept invalid topology"
}
