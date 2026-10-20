package nw

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/sarchlab/akita/v5/mem"
	"github.com/sarchlab/akita/v5/mem/vm"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/modeling"
	"github.com/sarchlab/akita/v5/noc/directconnection"
	"github.com/sarchlab/akita/v5/timing"
	"github.com/sarchlab/mgpusim/v5/amd/arch"
	"github.com/sarchlab/mgpusim/v5/amd/driver"
	"github.com/sarchlab/mgpusim/v5/amd/emu"
	"github.com/sarchlab/mgpusim/v5/amd/emu/cdna3"
	"github.com/sarchlab/mgpusim/v5/amd/emu/gcn3"
	"github.com/sarchlab/mgpusim/v5/amd/insts"
	"github.com/sarchlab/mgpusim/v5/amd/kernels"
	"github.com/sarchlab/mgpusim/v5/amd/protocol"
	"testing"
)

type nwTestStorage struct{ s *mem.Storage }

func (s nwTestStorage) Read(_ vm.PID, a, n uint64) []byte {
	b, e := s.s.Read(a, n)
	if e != nil {
		panic(e)
	}
	return b
}
func (s nwTestStorage) Write(_ vm.PID, a uint64, b []byte) {
	if e := s.s.Write(a, b); e != nil {
		panic(e)
	}
}
func TestNWCompiledWavefrontMatchesCPU(t *testing.T) {
	for _, architecture := range []arch.Type{arch.GCN3, arch.CDNA3} {
		for _, n := range []int{64, 128} {
			t.Run(fmt.Sprintf("%v-%d", architecture, n), func(t *testing.T) {
				b := &Benchmark{Arch: architecture, blockSize: 64, penalty: 10}
				b.SetLength(n)
				b.initData()
				b.loadProgram()
				store := nwTestStorage{mem.NewStorage(16 << 20)}
				put := func(a uint64, x interface{}) {
					var buf bytes.Buffer
					if e := binary.Write(&buf, binary.LittleEndian, x); e != nil {
						t.Fatal(e)
					}
					store.Write(1, a, buf.Bytes())
				}
				put(1<<20, b.reference)
				put(2<<20, b.inputItemSets)
				launch := func(co *insts.KernelCodeObject, index, blk int) {
					code := uint64(index * 65536)
					store.Write(1, code, co.Data)
					args := interface{}(&KernelArgs{Reference: 1 << 20, InputItemSets: 2 << 20, OutputItemSets: 3 << 20, LocalInputItemSets: 0, LocalReference: driver.LocalPtr(65 * 65 * 4), Cols: int32(n + 1), Penalty: 10, Blk: int32(blk), BlockSize: 64, BlockWidth: int32(n / 64), WorkSize: int32(n)})
					lds := uint32(65*65*4 + 64*64*4)
					if architecture == arch.CDNA3 {
						args = &CDNA3KernelArgs{Reference: 1 << 20, InputItemSets: 2 << 20, OutputItemSets: 3 << 20, Cols: int32(n + 1), Penalty: 10, Blk: int32(blk), BlockSize: 64, BlockWidth: int32(n / 64), WorkSize: int32(n)}
						lds = co.GroupSegmentByteSize
					}
					store.Write(1, 0x30000, make([]byte, co.KernargSegmentByteSize))
					put(0x30000, args)
					pkt := &kernels.HsaKernelDispatchPacket{GridSizeX: uint32(64 * blk), GridSizeY: 1, GridSizeZ: 1, WorkgroupSizeX: 64, WorkgroupSizeY: 1, WorkgroupSizeZ: 1, KernelObject: code, KernargAddress: 0x30000, GroupSegmentSize: lds}
					put(0x40000, pkt)
					grid := kernels.NewGridBuilder()
					grid.SetKernel(kernels.KernelLaunchInfo{CodeObject: co, Packet: pkt, PacketAddr: 0x40000})
					for wg := grid.NextWG(); wg != nil; wg = grid.NextWG() {
						engine := timing.NewSerialEngine()
						reg := modeling.NewStandaloneRegistrar(engine)
						decoder := insts.NewDisassembler()
						decoder.IsCDNA3 = architecture == arch.CDNA3
						var alu emu.ALU = gcn3.NewALU(store)
						if architecture == arch.CDNA3 {
							alu = cdna3.NewALU(store)
						}
						cu := emu.MakeBuilder().WithRegistrar(reg).WithResources(emu.Resources{Decoder: decoder, ALU: alu, StorageAccessor: store}).Build("NWTestCU")
						port := messaging.NewPort(cu, 8, 8, "NWTestCU.Dispatch")
						cu.AssignPort(emu.DispatchPortName, port)
						dispatcher := messaging.NewPort(nil, 8, 8, "NWTest.Dispatcher")
						conn := directconnection.MakeBuilder().WithRegistrar(reg).Build("NWTestConn")
						conn.PlugIn(port)
						conn.PlugIn(dispatcher)
						id := timing.GetIDGenerator().Generate()
						dispatcher.Send(protocol.MapWGReq{MsgMeta: messaging.MsgMeta{ID: id, Src: dispatcher.AsRemote(), Dst: port.AsRemote()}, WorkGroup: wg, PID: 1})
						if e := engine.Run(); e != nil {
							t.Fatal(e)
						}
						if dispatcher.RetrieveIncoming() == nil {
							t.Fatal("no WG completion")
						}
					}
				}
				for blk := 1; blk <= n/64; blk++ {
					launch(b.kernel1, 1, blk)
				}
				for blk := n/64 - 1; blk >= 1; blk-- {
					launch(b.kernel2, 2, blk)
				}
				b.cpuNW()
				raw := store.Read(1, 2<<20, uint64(len(b.inputItemSets)*4))
				got := make([]int32, len(b.inputItemSets))
				if e := binary.Read(bytes.NewReader(raw), binary.LittleEndian, &got); e != nil {
					t.Fatal(e)
				}
				for i, want := range b.inputItemSets {
					if got[i] != want {
						t.Fatalf("cell (%d,%d): got %d want %d", i/(n+1), i%(n+1), got[i], want)
					}
				}
			})
		}
	}
}
func TestNWGCNArgumentABIAndLengthValidation(t *testing.T) {
	b := &Benchmark{Arch: arch.GCN3}
	b.loadProgram()
	if got := binary.Size(KernelArgs{}); uint64(got) != b.kernel1.KernargSegmentByteSize {
		t.Fatalf("GCN argument size %d want %d", got, b.kernel1.KernargSegmentByteSize)
	}
	for _, n := range []int{0, 63, 65} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("invalid length %d was silently accepted", n)
				}
			}()
			b.SetLength(n)
		}()
	}
}
func TestNWCoreAllocationPreservesManagedPlacement(t *testing.T) {
	engine := timing.NewSerialEngine()
	d := driver.MakeBuilder().WithRegistrar(modeling.NewStandaloneRegistrar(engine)).Build("NWTestDriver")
	for i := 1; i <= 4; i++ {
		d.RegisterGPU(messaging.RemotePort(fmt.Sprintf("GPU[%d]", i)), driver.DeviceProperties{CUCount: 1, DRAMSize: 4 * mem.GB})
	}
	unified := d.CreateUnifiedGPU(nil, []int{1, 2, 3, 4})
	b := NewBenchmark(d)
	b.SelectGPU([]int{unified})
	b.SetUnifiedMemory()
	calls := 0
	d.ReplicaHooks = &driver.ReplicaHooks{Allocate: func(_ *driver.Context, n uint64) driver.Ptr {
		calls++
		if n != uint64(b.row*b.col*4) {
			t.Fatal("wrong allocation size")
		}
		return driver.Ptr(calls << 20)
	}}
	// Distribute here would try to remap these hook-managed addresses onto the
	// virtual unified device instead of preserving the physical home partition.
	b.allocateGPUMem()
	if calls != 3 {
		t.Fatal("managed buffers bypassed the allocation hook")
	}
	if engine.CurrentTime() != 0 {
		t.Fatal("allocation test executed simulated work")
	}
}
