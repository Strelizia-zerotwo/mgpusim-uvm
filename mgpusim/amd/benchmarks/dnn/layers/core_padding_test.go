package layers

import (
	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/dnn/tensor"
	"testing"
)

func TestCoreSmallVGGPaddedConvolution(t *testing.T) {
	op := &tensor.CPUOperator{}
	layer := NewConv2D(0, op, []int{1, 2, 2}, []int{1, 1, 3, 3}, []int{1, 1}, []int{1, 1})
	out := layer.Forward(op.Zeros([]int{1, 1, 2, 2}))
	if out.Size()[2] != 2 || out.Size()[3] != 2 {
		t.Fatal(out.Size())
	}
}
