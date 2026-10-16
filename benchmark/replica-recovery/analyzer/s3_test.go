package main

import "testing"

func TestS3CountsOnlyPreexistingReplicaReaders(t *testing.T) {
	s := newSim(4096, []uint64{32, 128, 512, 4096}, []kernel{{}, {}, {}}, nil)
	// Epoch 0: GPU1 home and GPU2 both read. GPU3 has never held a copy.
	s.epoch(0, map[uint64]*secAcc{0: {r: 6, rc: [maxGPU]uint32{0, 1, 1}}})
	// Epoch 1: GPU1 writes sector 0, GPUs2 and3 read clean sector 1.
	s.epoch(1, map[uint64]*secAcc{0: {w: 2, wc: [maxGPU]uint32{0, 1}}, 1: {r: 12, rc: [maxGPU]uint32{0, 0, 5, 7}}})
	if s.s3Eligible != 5 || s.s3NoReplica != 7 {
		t.Fatalf("eligible=%d no-copy=%d", s.s3Eligible, s.s3NoReplica)
	}
	// Epoch 2: GPU2 rereads; only its actually read dirty sector counts as intersection.
	s.epoch(2, map[uint64]*secAcc{0: {r: 4, rc: [maxGPU]uint32{0, 0, 1}}, 2: {r: 4}})
	if len(s.refills) != 1 || s.refills[0].readDirtySectors != 1 || s.refills[0].readCleanSectors != 1 {
		t.Fatalf("bad intersection: %+v", s.refills)
	}
}
