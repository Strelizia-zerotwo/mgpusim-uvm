package faultvm

import (
	"github.com/sarchlab/akita/v5/mem/vm"
	"github.com/sarchlab/akita/v5/mem/vm/vmprotocol"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/modeling"
	"github.com/sarchlab/akita/v5/timing"
	"github.com/sarchlab/mgpusim/v5/amd/timing/localmapping"
)

// BuildGPU creates a local GMMU and its Top and Host ports.
func BuildGPU(reg modeling.Registrar, name string, spec GPUSpec, table *localmapping.View) *GPU {
	spec.Config().Validate()
	if table == nil || table.GetLog2PageSize() != spec.Log2PageSize || spec.Host == "" {
		panic("faultvm: missing table, mismatched page size, or missing host")
	}
	c := modeling.NewBuilder[GPUSpec, GPUState, GPUResources]().
		WithEngine(reg.GetEngine()).WithFreq(timing.GHz).WithSpec(spec).
		WithResources(GPUResources{Table: table}).Build(name)
	c.DeclarePort("Top", vmprotocol.Responder)
	c.DeclarePort("Host", faultProtocol.Role("requester"))
	c.AddMiddleware(&gpuMiddleware{comp: c})
	reg.RegisterComponent(c)
	assignPort(reg, c, "Top", spec.Config().MaxTransactions)
	assignPort(reg, c, "Host", 1)
	return c
}

// BuildHost creates the shared host and a one-message ingress/egress port.
// The configured waiting queue is separate from this transport buffer.
func BuildHost(reg modeling.Registrar, name string, config Config, table vm.PageTable) *Host {
	config.Validate()
	if table == nil {
		panic("faultvm: missing authoritative table")
	}
	c := modeling.NewBuilder[Config, HostState, HostResources]().
		WithEngine(reg.GetEngine()).WithFreq(timing.GHz).WithSpec(config).
		WithResources(HostResources{Table: table}).Build(name)
	c.DeclarePort("Top", faultProtocol.Role("responder"))
	c.AddMiddleware(&hostMiddleware{comp: c})
	reg.RegisterComponent(c)
	assignPort(reg, c, "Top", 1)
	return c
}

func assignPort(reg modeling.Registrar, comp messaging.Component, name string, capacity int) {
	p := modeling.MakePortBuilder().WithRegistrar(reg).WithComponent(comp).
		WithSpec(modeling.PortSpec{BufSize: capacity}).Build(name)
	comp.AssignPort(name, p)
}

func duration(cycles int) timing.VTimeInPicoSec { return timing.VTimeInPicoSec(cycles) * Cycle }

func peak(value *uint64, candidate int) {
	if uint64(candidate) > *value {
		*value = uint64(candidate)
	}
}

func addLatency(sum, maximum *timing.VTimeInPicoSec, elapsed timing.VTimeInPicoSec) {
	*sum += elapsed
	if elapsed > *maximum {
		*maximum = elapsed
	}
}
