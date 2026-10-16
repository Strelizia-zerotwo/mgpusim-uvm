// Package lenet implements lenet network training.
package lenet

import (
	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/dnn/dataset/synthetic"
	"log"
	"math"

	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/dnn/gputensor"
	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/dnn/tensor"
	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/mccl"

	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/dnn/dataset/mnist"
	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/dnn/gputraining"
	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/dnn/layers"
	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/dnn/training"
	"github.com/sarchlab/mgpusim/v5/amd/benchmarks/dnn/training/optimization"
	"github.com/sarchlab/mgpusim/v5/amd/driver"
)

// Benchmark defines the LeNet network training benchmark.
type Benchmark struct {
	initialParams [][]float64
	driver        *driver.Driver
	ctx           *driver.Context
	to            []*gputensor.GPUOperator
	gpus          []int
	contexts      []*driver.Context

	networks []training.Network
	trainer  gputraining.DataParallelismMultiGPUTrainer

	BatchSize          int
	Epoch              int
	MaxBatchPerEpoch   int
	EnableVerification bool
	EnableTesting      bool
}

// NewBenchmark creates a new benchmark.
func NewBenchmark(driver *driver.Driver) *Benchmark {
	b := new(Benchmark)

	b.driver = driver
	b.ctx = driver.Init()

	return b
}

// SelectGPU selects the GPU to use.
func (b *Benchmark) SelectGPU(gpuIDs []int) {
	b.gpus = gpuIDs
}

func (b *Benchmark) init() {
	for _, gpu := range b.gpus {
		b.defineNetwork(gpu)
	}

	b.createTrainer()
	b.randomizeParams()
}

func (b *Benchmark) defineNetwork(gpuID int) {
	context := b.driver.InitWithExistingPID(b.ctx)
	b.driver.SelectGPU(context, gpuID)
	to := gputensor.NewGPUOperator(b.driver, context)

	if b.EnableVerification {
		to.EnableVerification()
	}

	network := b.makeNetwork(to)

	b.networks = append(b.networks, network)
	b.contexts = append(b.contexts, context)
	b.to = append(b.to, to)
}

func (b *Benchmark) createTrainer() {
	sources := make([]training.DataSource, len(b.networks))
	alg := make([]optimization.Alg, len(b.networks))
	testers := make([]*training.Tester, len(b.networks))
	lossFuncs := make([]training.LossFunction, len(b.networks))

	for i := 0; i < len(b.networks); i++ {
		if synthetic.Samples() > 0 {
			sources[i] = synthetic.New(b.to[i], []int{1, 28, 28}, 10)
		} else {
			sources[i] = mnist.NewTrainingDataSource(b.to[i])
		}
		alg[i] = optimization.NewAdam(b.to[i], 0.001)
		lossFuncs[i] = training.NewSoftmaxCrossEntropy(b.to[i])

		if b.EnableTesting {
			testers[i] = &training.Tester{
				DataSource: mnist.NewTestDataSource(b.to[i]),
				Network:    b.networks[i],
				BatchSize:  math.MaxInt32,
			}
		}
	}

	b.trainer = gputraining.DataParallelismMultiGPUTrainer{
		TensorOperators:  b.to,
		DataSource:       sources,
		Networks:         b.networks,
		LossFunc:         lossFuncs,
		OptimizationAlg:  alg,
		Tester:           testers,
		Epoch:            b.Epoch,
		MaxBatchPerEpoch: b.MaxBatchPerEpoch,
		BatchSize:        b.BatchSize,
		ShowBatchInfo:    true,
		GPUs:             b.gpus,
		Contexts:         b.contexts,
		Driver:           b.driver,
	}
}

func (b *Benchmark) randomizeParams() {
	initNet := b.networks[0]
	for _, l := range initNet.Layers {
		l.Randomize()
	}

	gpuNum := len(b.networks)
	if gpuNum == 1 {
		return
	}

	for i := range b.networks[0].Layers {
		if b.networks[0].Layers[i].Parameters() == nil {
			continue
		}

		params := make([]*gputensor.Tensor, gpuNum)
		datas := make([]driver.Ptr, gpuNum)

		for j := 0; j < gpuNum; j++ {
			params[j] = b.networks[j].Layers[i].Parameters().(*gputensor.Tensor)
		}

		dataSizeArr := params[0].Size()
		dataSize := 1
		for i := 0; i < len(dataSizeArr); i++ {
			dataSize *= dataSizeArr[i]
		}

		for i := 0; i < len(params); i++ {
			datas[i] = params[i].Ptr()
		}
		comms := mccl.CommInitAllMultipleContexts(
			gpuNum, b.driver, b.contexts, b.gpus)
		mccl.BroadcastRing(b.driver, comms, 1, datas, dataSize)
	}
}

// Run executes the benchmark.
func (b *Benchmark) Run() {
	b.init()
	if b.driver.ReplicaHooks != nil && b.EnableVerification {
		b.initialParams = training.CaptureParameters(b.networks[0])
	}
	b.trainer.Train()
}

// Verify runs the benchmark on the CPU and checks the result.
func (b *Benchmark) Verify() {
	if b.driver.ReplicaHooks != nil && b.EnableVerification {
		op := &tensor.CPUOperator{}
		var source training.DataSource
		if synthetic.Samples() > 0 {
			source = synthetic.New(op, []int{1, 28, 28}, 10)
		} else {
			source = mnist.NewTrainingDataSource(op)
		}
		training.ValidateTraining(b.networks[0], b.makeNetwork(op), b.initialParams, source, op, b.Epoch, b.BatchSize, b.MaxBatchPerEpoch)
		log.Print("Passed! independent CPU training and parameter validation")
		return
	}
	panic("not implemented")
}

// SetUnifiedMemory asks the benchmark to use unified memory.
func (b *Benchmark) SetUnifiedMemory() {
	if b.driver.ReplicaHooks != nil {
		return
	}
	panic("unified memory is not supported by dnn workloads")
}

func (b *Benchmark) makeNetwork(to tensor.Operator) training.Network {
	return training.Network{
		Layers: []layers.Layer{
			layers.NewConv2D(
				0,
				to,
				[]int{1, 28, 28},
				[]int{6, 1, 5, 5},
				[]int{1, 1},
				[]int{2, 2},
			),
			layers.NewReluLayer(to),
			layers.NewAvgPoolingLayer(
				to,
				[]int{2, 2},
				[]int{0, 0},
				[]int{2, 2},
			),
			layers.NewConv2D(
				3,
				to,
				[]int{6, 14, 14},
				[]int{16, 6, 5, 5},
				[]int{1, 1},
				[]int{0, 0}),
			layers.NewReluLayer(to),
			layers.NewAvgPoolingLayer(to,
				[]int{2, 2},
				[]int{0, 0},
				[]int{2, 2}),
			layers.NewFullyConnectedLayer(6, to, 400, 120),
			layers.NewReluLayer(to),
			layers.NewFullyConnectedLayer(8, to, 120, 84),
			layers.NewReluLayer(to),
			layers.NewFullyConnectedLayer(10, to, 84, 10),
		},
	}

}
