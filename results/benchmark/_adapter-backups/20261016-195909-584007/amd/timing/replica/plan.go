package replica

import (
	"encoding/json"
	"os"
	"sort"
)

// Access preserves a per-GPU per-page read/write mask and transaction counts.
// Offsets refer to sectors, not assumed value changes.
type Access struct {
	Page              uint64   `json:"page"`
	GPU               int      `json:"gpu"`
	Reads             []uint32 `json:"reads"`
	Writes            []uint32 `json:"writes"`
	ReadTransactions  uint64   `json:"read_transactions"`
	WriteTransactions uint64   `json:"write_transactions"`
}

// Epoch is an explicit global kernel boundary, possibly with concurrent GPUs.
type Epoch struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind,omitempty"`
	Accesses []Access `json:"accesses"`
	KernelPS uint64   `json:"kernel_ps"`
}

// Plan is reproducible only with the same workload, addresses and partitioning.
type Plan struct {
	Format      int     `json:"format"`
	PageBytes   uint64  `json:"page_bytes"`
	SectorBytes uint64  `json:"sector_bytes"`
	Epochs      []Epoch `json:"epochs"`
}

// Load reads a versioned oracle plan.
func Load(path string) *Plan {
	b, err := os.ReadFile(path)
	must(err)
	p := new(Plan)
	must(json.Unmarshal(b, p))
	if p.Format != 1 {
		panic("replica: unsupported plan format")
	}
	return p
}

// Save publishes a complete trace/plan after successful execution.
func (p *Plan) Save(path string) { writeJSON(path, p) }
func writeJSON(path string, v any) {
	b, e := json.MarshalIndent(v, "", "  ")
	must(e)
	must(os.WriteFile(path, append(b, '\n'), 0o644))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func set(xs []uint32) map[uint32]bool {
	m := map[uint32]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}
func sorted(m map[uint32]bool) []uint32 {
	xs := make([]uint32, 0, len(m))
	for x := range m {
		xs = append(xs, x)
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	return xs
}

// Validate checks plan shape. Pages with multiple writers or conservative
// read/write overlap are handled by page-local fallback, not rejected globally.
func (p *Plan) Validate() {
	if p.PageBytes < 4096 || p.SectorBytes < 32 || p.PageBytes%p.SectorBytes != 0 {
		panic("replica: malformed plan geometry")
	}
	for _, e := range p.Epochs {
		seen := map[[2]uint64]bool{}
		for _, a := range e.Accesses {
			k := [2]uint64{a.Page, uint64(a.GPU)}
			if a.GPU < 1 || a.Page%p.PageBytes != 0 || seen[k] {
				panic("replica: invalid or duplicate access")
			}
			seen[k] = true
			for _, xs := range [][]uint32{a.Reads, a.Writes} {
				for _, x := range xs {
					if uint64(x) >= p.PageBytes/p.SectorBytes {
						panic("replica: sector outside page")
					}
				}
			}
		}
	}
}

// Granularity reports the required byte count and contiguous transfer runs.
type Granularity struct {
	Bytes   uint64 `json:"bytes"`
	Blocks  uint64 `json:"blocks"`
	Extents uint64 `json:"extents"`
}

func intervals(mask map[uint32]bool, sector, gran uint64) Granularity {
	m := map[uint32]bool{}
	for s := range mask {
		m[uint32(uint64(s)*sector/gran)] = true
	}
	xs := sorted(m)
	out := Granularity{Bytes: uint64(len(xs)) * gran, Blocks: uint64(len(xs))}
	for i, x := range xs {
		if i == 0 || x != xs[i-1]+1 {
			out.Extents++
		}
	}
	return out
}
