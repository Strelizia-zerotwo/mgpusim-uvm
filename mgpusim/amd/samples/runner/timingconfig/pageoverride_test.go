package timingconfig

import "testing"

func TestExplicitPageSizeSurvivesGPUDefaults(t *testing.T) {
	for _, gpu := range []string{"r9nano", "mi300x"} {
		for _, size := range []uint64{12, 16, 21} {
			b := MakeBuilder().WithGPUType(gpu).WithLog2PageSize(size)
			b.adjustConfigForGPUType()
			if b.log2PageSize != size {
				t.Fatalf("%s page override %d became %d", gpu, size, b.log2PageSize)
			}
		}
	}
}

func TestDefaultPageSizesRemainGPUDefaults(t *testing.T) {
	for gpu, want := range map[string]uint64{"r9nano": 12, "mi300x": 21} {
		b := MakeBuilder().WithGPUType(gpu)
		b.adjustConfigForGPUType()
		if b.log2PageSize != want {
			t.Fatalf("%s default page size %d want %d", gpu, b.log2PageSize, want)
		}
	}
}
