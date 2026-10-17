package gaussian

import (
	"fmt"
	"github.com/sarchlab/akita/v5/mem"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/modeling"
	"github.com/sarchlab/akita/v5/timing"
	"github.com/sarchlab/mgpusim/v5/amd/driver"
	"github.com/sarchlab/mgpusim/v5/amd/timing/localmapping"
	"testing"
)

func TestCommandQueueFollowsSelectedPhysicalOrUnifiedDevice(t *testing.T) {
	engine := timing.NewSerialEngine()
	d := driver.MakeBuilder().WithRegistrar(modeling.NewStandaloneRegistrar(engine)).WithResources(driver.Resources{PageTable: localmapping.NewShared(12), GlobalStorage: mem.NewStorage(32 * mem.GB)}).Build("Driver")
	for i := 1; i <= 4; i++ {
		d.RegisterGPU(messaging.RemotePort(fmt.Sprintf("GPU[%d]", i)), driver.DeviceProperties{CUCount: 1, DRAMSize: 4 * mem.GB})
	}
	unified := d.CreateUnifiedGPU(nil, []int{1, 2, 3, 4})
	for _, target := range []int{3, unified} {
		b := NewBenchmark(d)
		if b.queue != nil {
			t.Fatal("queue created before selecting target device")
		}
		b.SelectGPU([]int{target})
		b.initCommandQueue()
		b.loadProgram()
		if b.queue.GPUID != target || b.queue.Context != b.context {
			t.Fatalf("queue device %d want %d", b.queue.GPUID, target)
		}
	}
	if engine.CurrentTime() != 0 {
		t.Fatal("host setup test executed simulated work")
	}
}
