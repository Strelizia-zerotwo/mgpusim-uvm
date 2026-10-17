// replica-internal executes one real kernel with repeated loads and modifying
// stores on four disjoint ranges of the SAME page. There is no kernel boundary
// between repetitions. Every output value is checked against a CPU reference.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"log"

	"github.com/sarchlab/mgpusim/v5/amd/driver"
	"github.com/sarchlab/mgpusim/v5/amd/insts"
	"github.com/sarchlab/mgpusim/v5/amd/samples/runner"
)

var copyKernelPath = flag.String("copy-kernel", "amd/driver/memcopy.hsaco", "Path to the repository's GCN3 copyKernel fixture")
var rounds = flag.Int("rounds", 4, "Read/modify/write repetitions INSIDE one kernel")

type benchmark struct {
	d            *driver.Driver
	ctx          *driver.Context
	gpus         []int
	ptr          driver.Ptr
	initial, got []uint32
}

func (b *benchmark) SelectGPU(ids []int) { b.gpus = ids }
func (*benchmark) SetUnifiedMemory()     {}
func (b *benchmark) Run() {
	if *rounds < 3 || *rounds > 32 {
		panic("rounds must be 3..32")
	}
	b.ctx = b.d.Init()
	b.d.SelectGPU(b.ctx, b.gpus[0])
	b.initial = make([]uint32, 256)
	b.got = make([]uint32, len(b.initial))
	for i := range b.initial {
		b.initial[i] = uint32(i*17 + 3)
	}
	b.ptr = b.d.AllocateUnifiedMemory(b.ctx, 4096)
	b.d.MemCopyH2D(b.ctx, b.ptr, b.initial)
	kernel := makeKernel(*copyKernelPath, *rounds)
	// Four 64-thread workgroups, one contiguous range per physical GPU.
	args := struct {
		Src, Dst driver.Ptr
		Bytes    int64
	}{b.ptr, b.ptr, int64(len(b.initial) * 4)}
	b.d.LaunchKernel(b.ctx, kernel, [3]uint32{256, 1, 1}, [3]uint16{64, 1, 1}, &args)
	b.d.MemCopyD2H(b.ctx, b.got, b.ptr)
}
func (b *benchmark) Verify() {
	for i, want := range b.initial {
		want += uint32(*rounds)
		if b.got[i] != want {
			panic(fmt.Sprintf("single-kernel stale data at %d: got %d want %d", i, b.got[i], want))
		}
	}
	log.Printf("Passed! single kernel, %d rounds of actual modifying loads/stores; every output checked", *rounds)
}

func makeKernel(path string, n int) *insts.KernelCodeObject {
	k := insts.LoadKernelCodeObjectFromFS(path, "copyKernel")
	// Validate the exact reused fixture before editing its instruction stream.
	if len(k.Data) < 0x8c || !bytes.Equal(k.Data[0x80:0x8c], []byte{0, 0, 0x70, 0xdc, 0, 2, 0, 0, 0, 0, 0x81, 0xbf}) {
		panic("unexpected copyKernel fixture")
	}
	code := append([]byte(nil), k.Data[:0x80]...)
	word := func(v uint32) {
		buf := make([]byte, 4)
		binary.LittleEndian.PutUint32(buf, v)
		code = append(code, buf...)
	}
	// After the original setup: v[0:1]=destination address, v2=loaded value.
	addStore := func() { word(0x32040481); word(0xdc700000); word(0x00000200); word(0xbf8c0000) }
	addStore()
	for i := 1; i < n; i++ {
		word(0xdc500000)
		word(0x02000000) // flat_load_dword v2, v[0:1]
		word(0xbf8c0000) // wait for the ACTUAL load response
		addStore()       // v2 += 1; store; wait before next load
	}
	end := len(code)
	word(0xbf810000)
	// The fixture's out-of-range branch must skip the expanded body too.
	branch := binary.LittleEndian.Uint32(code[0x3c:0x40])
	branch = branch&0xffff0000 | uint32((end-0x40)/4)
	binary.LittleEndian.PutUint32(code[0x3c:0x40], branch)
	k.Data = code
	k.Symbol.Size = uint64(len(code))
	// Decode every instruction now, so malformed edits fail before simulation.
	decoder := insts.NewDisassembler()
	for i := 0; i < len(code); {
		v, err := decoder.Decode(code[i:])
		if err != nil {
			panic(err)
		}
		i += v.ByteSize
	}
	return k
}
func main() {
	flag.Parse()
	r := new(runner.Runner).Init()
	r.AddBenchmark(&benchmark{d: r.Driver()})
	r.Run()
}
