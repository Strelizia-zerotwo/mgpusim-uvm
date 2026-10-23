package replica

import (
	"encoding/json"
	"github.com/sarchlab/mgpusim/v5/amd/driver"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProgressDiagnosticsDoNotScheduleOrAdvanceSimulation(t *testing.T) {
	f := idealTest("inv")
	f.p.s.d = &driver.Driver{}
	path := filepath.Join(t.TempDir(), "progress.jsonl")
	if err := f.p.EnableProgress(path, time.Hour); err != nil {
		t.Fatal(err)
	}
	f.p.publishProgress("kernel-start", true)
	// Store one real pending copy in the immutable sample, then let the
	// diagnostics read it. Reading a sample must not complete the transfer.
	r := readReq(4096)
	if f.p.admit(2, r) {
		t.Fatal("read should wait for real service")
	}
	if f.p.progress.sample.Load().Transfers != 1 {
		t.Fatal("first blocked read was not included in progress snapshot")
	}
	before, jobs := f.now, len(f.jobs)
	for i := 0; i < 3; i++ {
		f.p.progress.record("periodic")
	}
	if f.now != before || len(f.jobs) != jobs || len(f.p.transfers) != 1 {
		t.Fatal("diagnostics changed simulated work")
	}
	if _, err := os.Stat(path + ".stacks.txt"); err != nil {
		t.Fatal("idle diagnostic did not save stacks", err)
	}
	f.p.closeProgress()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var first, second progressRecord
	if err := decoder.Decode(&first); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&second); err != nil {
		t.Fatal(err)
	}
	if first.Kind != "kernel-start" || second.Transfers != 1 || second.Kernel != 1 {
		t.Fatalf("wrong progress samples: %+v %+v", first, second)
	}
}
