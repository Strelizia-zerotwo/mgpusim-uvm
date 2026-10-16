package replica

import (
	"reflect"
	"testing"
)

func TestCoreTracePreservesCrossPageMasks(t *testing.T) {
	s := &Session{Config: DefaultConfig(), pages: map[uint64]*pageState{4096: {}, 8192: {}}, observed: map[[2]uint64]*Access{}}
	s.observe(2, 8192-16, 64, nil, false)
	if got := sorted(s.observedReads[[2]uint64{4096, 2}]); !reflect.DeepEqual(got, []uint32{127}) {
		t.Fatal(got)
	}
	if got := sorted(s.observedReads[[2]uint64{8192, 2}]); !reflect.DeepEqual(got, []uint32{0, 1}) {
		t.Fatal(got)
	}
	mask := make([]bool, 64)
	mask[0] = true
	mask[63] = true
	s.observe(2, 8192-16, 64, mask, true)
	if got := sorted(s.observedWrites[[2]uint64{4096, 2}]); !reflect.DeepEqual(got, []uint32{127}) {
		t.Fatal(got)
	}
	if got := sorted(s.observedWrites[[2]uint64{8192, 2}]); !reflect.DeepEqual(got, []uint32{1}) {
		t.Fatal(got)
	}
}
