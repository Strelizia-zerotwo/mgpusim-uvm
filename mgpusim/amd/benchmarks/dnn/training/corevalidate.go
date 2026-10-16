package training

import (
	"fmt"
	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/dnn/tensor"
	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/dnn/training/optimization"
	"math"
)

// CaptureParameters preserves the actual initialized FP32 GPU parameters.
func CaptureParameters(net Network) [][]float64 {
	out := make([][]float64, len(net.Layers))
	for i, l := range net.Layers {
		if p := l.Parameters(); p != nil {
			out[i] = append([]float64(nil), p.Vector()...)
		}
	}
	return out
}

// ValidateTraining independently replays forward/backward/Adam on the CPU from
// the same initialized parameters and input sequence, then checks every weight.
func ValidateTraining(gpu, cpu Network, initial [][]float64, source DataSource, op *tensor.CPUOperator, epochs, batch, maxBatches int) {
	for i, l := range cpu.Layers {
		if p := l.Parameters(); p != nil {
			op.Init(p, initial[i])
		}
	}
	alg := optimization.NewAdam(op, 0.001)
	loss := NewSoftmaxCrossEntropy(op)
	for epoch := 0; epoch < epochs; epoch++ {
		source.Rewind()
		for n := 0; n < maxBatches; n++ {
			x, y := source.NextBatch(batch)
			if len(y) == 0 {
				break
			}
			for _, l := range cpu.Layers {
				x = l.Forward(x)
			}
			_, dx := loss.Loss(x, y)
			for j := len(cpu.Layers) - 1; j >= 0; j-- {
				dx = cpu.Layers[j].Backward(dx)
			}
			for _, l := range cpu.Layers {
				alg.UpdateParameters(l)
			}
		}
	}
	for i, l := range cpu.Layers {
		if p := l.Parameters(); p != nil {
			expected, actual := p.Vector(), gpu.Layers[i].Parameters().Vector()
			if len(expected) != len(actual) {
				panic("training parameter shape mismatch")
			}
			for j, v := range expected {
				if math.IsNaN(v) || math.IsInf(v, 0) || math.IsNaN(actual[j]) || math.IsInf(actual[j], 0) || math.Abs(v-actual[j]) > 1e-4+1e-2*math.Abs(v) {
					panic(fmt.Sprintf("training CPU mismatch layer=%d parameter=%d expected=%g actual=%g", i, j, v, actual[j]))
				}
			}
		}
	}
}
