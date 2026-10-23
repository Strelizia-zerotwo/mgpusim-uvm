package cudaports2

import (
	"fmt"
	"testing"

	"github.com/sarchlab/akita/v5/mem"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/modeling"
	"github.com/sarchlab/akita/v5/timing"
	"github.com/sarchlab/mgpusim/v5/amd/driver"
	"github.com/sarchlab/mgpusim/v5/amd/timing/localmapping"
)

func TestQueueBindsToSelectedPhysicalOrUnifiedGPU(t *testing.T) {
	engine := timing.NewSerialEngine()
	d := driver.MakeBuilder().WithRegistrar(modeling.NewStandaloneRegistrar(engine)).WithResources(driver.Resources{PageTable: localmapping.NewShared(12), GlobalStorage: mem.NewStorage(32 * mem.GB)}).Build("Driver")
	for i := 1; i <= 4; i++ {
		d.RegisterGPU(messaging.RemotePort(fmt.Sprintf("GPU[%d]", i)), driver.DeviceProperties{CUCount: 1, DRAMSize: 4 * mem.GB})
	}
	unified := d.CreateUnifiedGPU(nil, []int{1, 2, 3, 4})
	for _, target := range []int{3, unified} {
		b := NewBenchmark(d, "SSSP")
		if b.queue != nil {
			t.Fatal("queue created before selecting GPU")
		}
		b.SelectGPU([]int{target})
		b.initCommandQueue()
		if b.queue.GPUID != target || b.queue.Context != b.context {
			t.Fatal("wrong queue binding")
		}
	}
	if engine.CurrentTime() != 0 {
		t.Fatal("queue setup advanced simulated time")
	}
}
