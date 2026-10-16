package timingconfig

import (
	"fmt"

	"github.com/sarchlab/mgpusim/v5/amd/driver"
	"github.com/sarchlab/mgpusim/v5/amd/timing/faultvm"
	"github.com/sarchlab/mgpusim/v5/amd/timing/localmapping"
)

// WithFaultVM selects the demand-mapping hardware and shared host service.
func (b Builder) WithFaultVM(mode string, config faultvm.Config) Builder {
	if mode != "demand" {
		panic("faultvm: expected demand mode")
	}
	config.Validate()
	b.faultMode = mode
	b.faultConfig = config
	return b
}

func (b *Builder) buildFaultPlatform() *driver.Driver {
	tables := localmapping.NewDemandLocal(b.log2PageSize, b.numGPUs)
	tables.UseFixedPlacement()
	gpuDriver := b.buildGPUDriver(tables)
	conn := b.createConnection(gpuDriver, nil)
	host := faultvm.BuildHost(b.simulation, "HostMMU", b.faultConfig, tables)
	conn.PlugIn(host.GetPortByName("Top"))
	for index := 1; index <= b.numGPUs; index++ {
		mmuComp := faultvm.BuildGPU(b.simulation, fmt.Sprintf("GPU[%d].MMU", index),
			faultvm.MakeGPUSpec(b.faultConfig, host.GetPortByName("Top").AsRemote(), b.log2PageSize), tables.View(index-1))
		conn.PlugIn(mmuComp.GetPortByName("Top"))
		conn.PlugIn(mmuComp.GetPortByName("Host"))
		b.createGPU(index, b.createGPUBuilder(mmuComp, gpuDriver), gpuDriver, conn)
	}
	return gpuDriver
}
