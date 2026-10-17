// Package replica implements a kernel-barrier oracle experiment. The plan is
// recorded from executed GPU requests. It is not a hardware coherence protocol.
package replica

import (
	"fmt"
	"github.com/sarchlab/akita/v5/timing"
	"math"
	"strings"
	"sync"
)

// Config is shared by INV and all oracle comparisons; times are nanoseconds.
type Config struct {
	HasSystemScopeSync bool    `json:"has_system_scope_sync"`
	TransferGranBytes  uint64  `json:"transfer_gran_bytes"`
	Policy             string  `json:"policy"`
	PageBytes          uint64  `json:"page_bytes"`
	SectorBytes        uint64  `json:"sector_bytes"`
	FaultNS            uint64  `json:"fault_ns"`
	FaultSlots         int     `json:"fault_slots"`
	BandwidthGBs       float64 `json:"bandwidth_gbs"`
	LinkNS             uint64  `json:"link_ns"`
	ReplicaPagesPerGPU int     `json:"replica_pages_per_gpu"`
	MetadataNS         uint64  `json:"metadata_ns"`
	ShootdownNS        uint64  `json:"shootdown_ns"`
}

// DefaultConfig makes assumptions explicit rather than claiming calibration.
func DefaultConfig() Config {
	return Config{Policy: "inv", PageBytes: 4096, SectorBytes: 32, TransferGranBytes: 32, FaultNS: 900,
		FaultSlots: 16, BandwidthGBs: 300, LinkNS: 200}
}

// Validate refuses unsupported idealizations and malformed geometry.
func (c Config) Validate() {
	switch c.Policy {
	case "norep", "inv", "retain", "i1-no-s3", "i1", "i1-write-prefetch", "i1-full-stale", "i1-adaptive", "i2", "infinity":
	default:
		panic("replica: unknown policy")
	}
	if c.PageBytes < 4096 || c.PageBytes&(c.PageBytes-1) != 0 || c.SectorBytes < 32 ||
		c.SectorBytes&(c.SectorBytes-1) != 0 || c.PageBytes%c.SectorBytes != 0 ||
		c.FaultSlots < 1 || c.BandwidthGBs < 0 || math.IsNaN(c.BandwidthGBs) || math.IsInf(c.BandwidthGBs, 0) ||
		c.ReplicaPagesPerGPU < 0 || c.TransferGranBytes < c.SectorBytes || c.TransferGranBytes > c.PageBytes ||
		c.TransferGranBytes%c.SectorBytes != 0 || c.TransferGranBytes&(c.TransferGranBytes-1) != 0 {
		panic("replica: invalid configuration")
	}
	if c.Policy != "i2" && (c.MetadataNS != 0 || c.ShootdownNS != 0) {
		panic("replica: overheads require i2")
	}
}

// Fabric serializes source injection and destination reception at finite
// bandwidth. Different disjoint GPU pairs can overlap. It is a bandwidth/latency
// abstraction, not an NVSwitch topology or a calibrated NVLink implementation.
type Fabric struct {
	mu         sync.Mutex
	GBs        float64
	LatencyNS  uint64
	send, recv map[string]timing.VTimeInPicoSec
	Bytes      uint64
	Messages   uint64
}

// NewFabric returns a common fabric for both copy traffic and ordinary RDMA.
func NewFabric(c Config) *Fabric {
	return &Fabric{GBs: c.BandwidthGBs, LatencyNS: c.LinkNS,
		send: map[string]timing.VTimeInPicoSec{}, recv: map[string]timing.VTimeInPicoSec{}}
}

// Reserve reserves actual bytes. GB/s is decimal; zero bandwidth means infinite
// bandwidth for sensitivity analysis, while propagation latency remains charged.
func (f *Fabric) Reserve(src, dst string, n uint64, now timing.VTimeInPicoSec) timing.VTimeInPicoSec {
	f.mu.Lock()
	defer f.mu.Unlock()
	src = endpoint(src)
	dst = endpoint(dst)
	start := max(now, f.send[src], f.recv[dst])
	var transfer timing.VTimeInPicoSec
	if f.GBs > 0 {
		transfer = timing.VTimeInPicoSec(math.Ceil(float64(n) * 1000 / f.GBs))
	}
	finish := start + transfer
	f.send[src] = finish
	f.recv[dst] = finish
	f.Bytes += n
	f.Messages++
	return finish + timing.VTimeInPicoSec(f.LatencyNS)*1000
}
func gpuName(g int) string { return fmt.Sprintf("GPU[%d]", g) }

func endpoint(s string) string {
	if i := strings.IndexByte(s, ']'); i >= 0 {
		return s[:i+1]
	}
	return s
}
