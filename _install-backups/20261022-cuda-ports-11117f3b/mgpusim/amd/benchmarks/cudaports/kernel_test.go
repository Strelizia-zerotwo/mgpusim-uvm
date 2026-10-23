package cudaports

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
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

type testStorage struct{ storage *mem.Storage }

func (s testStorage) Read(_ vm.PID, a, n uint64) []byte {
	data, err := s.storage.Read(a, n)
	if err != nil {
		panic(err)
	}
	return data
}
func (s testStorage) Write(_ vm.PID, a uint64, data []byte) {
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
	for wg := grid.NextWG(); wg != nil; wg = grid.NextWG() {
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

func TestCompiledGraphAlgorithms(t *testing.T) {
	for mode, algorithm := range []string{"SSSP", "CC", "COLOR", "MIS"} {
		for _, n := range []int{17, 257} {
			t.Run(fmt.Sprintf("%s-%d", algorithm, n), func(t *testing.T) {
				g, err := GenerateGraph(n, 8, 3, "random", 42)
				if err != nil {
					t.Fatal(err)
				}
				state, priority := initialState(n, uint32(mode), 42)
				// Equal priorities also exercise the deterministic vertex-ID tie break.
				if n == 17 {
					for i := range priority {
						priority[i] = 1
					}
				}
				s := testStorage{mem.NewStorage(16 << 20)}
				s.put(t, 0x100000, g.Row)
				s.put(t, 0x200000, g.Col)
				s.put(t, 0x300000, g.Weight)
				s.put(t, 0x400000, state)
				s.put(t, 0x500000, priority)
				remaining, done := n, false
				for iteration := 0; iteration <= n; iteration++ {
					s.launch(t, "graph_gather", &GatherArgs{Row: 0x100000, Col: 0x200000, Weight: 0x300000, State: 0x400000, Priority: 0x500000, Proposal: 0x600000, N: uint32(n), Mode: uint32(mode), Iteration: uint32(iteration)}, n)
					s.launch(t, "graph_publish", &PublishArgs{Row: 0x100000, Col: 0x200000, State: 0x400000, Proposal: 0x600000, Changed: 0x700000, N: uint32(n), Mode: uint32(mode)}, n)
					changed := make([]uint32, n)
					s.get(t, 0x700000, changed, n*4)
					count := 0
					for _, x := range changed {
						count += int(x)
					}
					remaining -= count
					if (mode < 2 && count == 0) || (mode >= 2 && remaining == 0) {
						done = true
						break
					}
				}
				if !done {
					t.Fatal("failed to converge")
				}
				s.get(t, 0x400000, state, n*4)
				if err := verifyGraph(g, algorithm, state); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestCompiledEmbeddingDuplicateIDsAndMultipleBatches(t *testing.T) {
	const rows, dim, items, batches = 19, 8, 40, 3
	initial := make([]float32, rows*dim)
	for i := range initial {
		initial[i] = float32(i%23) / 32
	}
	ids := make([]uint32, items*batches)
	for i := range ids {
		ids[i] = uint32((i*7 + i/3) % rows)
	}
	s := testStorage{mem.NewStorage(16 << 20)}
	s.put(t, 0x100000, initial)
	s.put(t, 0x200000, ids)
	for k := 0; k < batches; k++ {
		a := &EmbeddingArgs{Table: 0x100000, IDs: 0x200000, Output: 0x300000, Dim: dim, LogDim: 3, Items: items, Offset: uint32(k * items), Step: 0.1 / items}
		s.launch(t, "embedding_lookup", a, items*dim)
		s.launch(t, "embedding_update", a, items*dim)
	}
	got := make([]float32, len(initial))
	s.get(t, 0x100000, got, len(got)*4)
	want := embeddingReference(initial, ids, dim, items, batches, 0.1)
	for i, x := range want {
		if math.Abs(float64(got[i]-x)) > 1e-6 || math.IsNaN(float64(got[i])) {
			t.Fatalf("element %d got %g want %g", i, got[i], x)
		}
	}
}

func TestGraphReferenceAndVerificationRejectInvalidResults(t *testing.T) {
	g := Graph{Row: []uint32{0, 1, 3, 4, 4}, Col: []uint32{1, 0, 2, 1}, Weight: []uint32{3, 3, 5, 5}}
	if err := verifyGraph(g, "SSSP", []uint32{0, 3, 8, infinity}); err != nil {
		t.Fatal(err)
	}
	if err := verifyGraph(g, "CC", []uint32{0, 0, 0, 3}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		algorithm string
		state     []uint32
	}{
		{"SSSP", []uint32{0, 3, 7, infinity}}, {"CC", []uint32{0, 0, 0, 0}},
		{"COLOR", []uint32{1, 1, 2, 0}}, {"MIS", []uint32{1, 1, 2, 1}}, {"MIS", []uint32{2, 2, 2, 1}},
	} {
		if verifyGraph(g, test.algorithm, test.state) == nil {
			t.Fatalf("accepted invalid %s result", test.algorithm)
		}
	}
}
