package cudaports2

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"testing"

	"github.com/sarchlab/akita/v5/mem"
	"github.com/sarchlab/akita/v5/mem/vm"
	"github.com/sarchlab/akita/v5/messaging"
	"github.com/sarchlab/akita/v5/modeling"
	"github.com/sarchlab/akita/v5/noc/directconnection"
	"github.com/sarchlab/akita/v5/timing"
	"github.com/sarchlab/mgpusim/v5/amd/emu"
	"github.com/sarchlab/mgpusim/v5/amd/emu/cdna3"
	"github.com/sarchlab/mgpusim/v5/amd/insts"
	"github.com/sarchlab/mgpusim/v5/amd/kernels"
	"github.com/sarchlab/mgpusim/v5/amd/protocol"
)

// Functional emulation has private register arrays per wavefront, so it cannot
// detect under-allocation in the timing CU. Verify the binary/loader contract.
func TestCompiledKernelsProvideTimingRegisterResources(t *testing.T) {
	file, err := elf.NewFile(bytes.NewReader(kernelBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	symbols, err := file.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]uint64{}
	for _, symbol := range symbols {
		values[symbol.Name] = symbol.Value
	}
	for _, name := range []string{"graph_inplace", "mis_mark", "mis_drop", "embedding_lookup", "embedding_group", "embedding_update"} {
		sgpr, okS := values[name+".numbered_sgpr"]
		vgpr, okV := values[name+".num_vgpr"]
		if !okS || !okV {
			t.Fatalf("%s: missing register allocation symbols", name)
		}
		co := kernel(name)
		if uint64(co.WFSgprCount) < sgpr+2 || uint64(co.WIVgprCount) < vgpr {
			t.Fatalf("%s: timing allocation SGPR/VGPR=%d/%d, needs >=%d/%d", name, co.WFSgprCount, co.WIVgprCount, sgpr+2, vgpr)
		}
	}
}

type testStorage struct {
	storage *mem.Storage
	reverse bool
	onWrite func(uint64, []byte)
}

func (s testStorage) Read(_ vm.PID, a, n uint64) []byte {
	data, err := s.storage.Read(a, n)
	if err != nil {
		panic(err)
	}
	return data
}
func (s testStorage) Write(_ vm.PID, a uint64, data []byte) {
	if s.onWrite != nil {
		s.onWrite(a, data)
	}
	if err := s.storage.Write(a, data); err != nil {
		panic(err)
	}
}
func (s testStorage) put(t *testing.T, address uint64, value interface{}) {
	t.Helper()
	var data bytes.Buffer
	if err := binary.Write(&data, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
	s.Write(1, address, data.Bytes())
}
func (s testStorage) get(t *testing.T, address uint64, value interface{}, size int) {
	t.Helper()
	if err := binary.Read(bytes.NewReader(s.Read(1, address, uint64(size))), binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}
func (s testStorage) launch(t *testing.T, name string, args interface{}, count int) {
	t.Helper()
	co := kernel(name)
	if binary.Size(args) < int(co.KernargSegmentByteSize) {
		t.Fatalf("%s: argument bytes %d < ABI %d", name, binary.Size(args), co.KernargSegmentByteSize)
	}
	s.Write(1, 0x10000, co.Data)
	s.put(t, 0x20000, args)
	pkt := &kernels.HsaKernelDispatchPacket{GridSizeX: uint32((count + 255) / 256 * 256), GridSizeY: 1, GridSizeZ: 1, WorkgroupSizeX: 256, WorkgroupSizeY: 1, WorkgroupSizeZ: 1, KernelObject: 0x10000, KernargAddress: 0x20000, GroupSegmentSize: co.GroupSegmentByteSize}
	s.put(t, 0x30000, pkt)
	grid := kernels.NewGridBuilder()
	grid.SetKernel(kernels.KernelLaunchInfo{CodeObject: co, Packet: pkt, PacketAddr: 0x30000})
	var groups []*kernels.WorkGroup
	for wg := grid.NextWG(); wg != nil; wg = grid.NextWG() {
		groups = append(groups, wg)
	}
	if s.reverse {
		for i, j := 0, len(groups)-1; i < j; i, j = i+1, j-1 {
			groups[i], groups[j] = groups[j], groups[i]
		}
	}
	for _, wg := range groups {
		engine := timing.NewSerialEngine()
		reg := modeling.NewStandaloneRegistrar(engine)
		decoder := insts.NewDisassembler()
		decoder.IsCDNA3 = true
		cu := emu.MakeBuilder().WithRegistrar(reg).WithResources(emu.Resources{Decoder: decoder, ALU: cdna3.NewALU(s), StorageAccessor: s}).Build("CUDAPortTestCU")
		port := messaging.NewPort(cu, 8, 8, "CUDAPortTestCU.Dispatch")
		cu.AssignPort(emu.DispatchPortName, port)
		dispatcher := messaging.NewPort(nil, 8, 8, "CUDAPortTest.Dispatcher")
		connection := directconnection.MakeBuilder().WithRegistrar(reg).Build("CUDAPortTestConnection")
		connection.PlugIn(port)
		connection.PlugIn(dispatcher)
		dispatcher.Send(protocol.MapWGReq{MsgMeta: messaging.MsgMeta{ID: timing.GetIDGenerator().Generate(), Src: dispatcher.AsRemote(), Dst: port.AsRemote()}, WorkGroup: wg, PID: 1})
		if err := engine.Run(); err != nil {
			t.Fatal(err)
		}
		if dispatcher.RetrieveIncoming() == nil {
			t.Fatal("no completed workgroup")
		}
	}
}
