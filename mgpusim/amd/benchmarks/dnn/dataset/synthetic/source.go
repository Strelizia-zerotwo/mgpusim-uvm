// Package synthetic supplies explicitly selected deterministic training inputs.
package synthetic

import (
	"flag"
	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/dnn/tensor"
)

var count = flag.Int("dnn-synthetic-samples", 0, "Deterministic synthetic training samples; 0 uses the real dataset")

func Samples() int { return *count }

type Source struct {
	op          tensor.Operator
	shape       []int
	classes, at int
}

func New(op tensor.Operator, shape []int, classes int) *Source {
	return &Source{op: op, shape: shape, classes: classes}
}
func (s *Source) Rewind() { s.at = 0 }
func (s *Source) NextBatch(batch int) (tensor.Tensor, []int) {
	n := min(batch, *count-s.at)
	if n <= 0 {
		return nil, nil
	}
	elements := 1
	for _, x := range s.shape {
		elements *= x
	}
	data := make([]float64, n*elements)
	labels := make([]int, n)
	for i := range data {
		data[i] = float64(((s.at*elements+i)*17+13)%251) / 250
	}
	for i := range labels {
		labels[i] = (s.at + i) % s.classes
	}
	dims := append([]int{n}, s.shape...)
	s.at += n
	return s.op.CreateWithData(data, dims, "NCHW"), labels
}
