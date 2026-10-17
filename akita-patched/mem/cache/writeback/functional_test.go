package writeback

import (
	"bytes"
	"github.com/sarchlab/akita/v5/mem"
	"testing"
)

func TestFunctionalCoherencePreservesUnrelatedLinesAndInflightOwnership(t *testing.T) {
	c, _, control := rangeTestCache()
	b := &c.State.DirectoryState.Sets[0].Blocks[0]
	other := &c.State.DirectoryState.Sets[1].Blocks[0]
	b.Tag = 4096
	b.IsValid = true
	b.IsDirty = true
	b.IsLocked = true
	b.ReadCount = 2
	b.DirtyMask = make([]bool, 64)
	b.DirtyMask[1] = true
	b.DirtyMask[2] = true
	other.Tag = 8192
	other.IsValid = true
	other.IsDirty = true
	data := bytes.Repeat([]byte{7}, 64)
	if e := c.Resources().Storage.Write(b.CacheAddress, data); e != nil {
		t.Fatal(e)
	}
	backing := mem.NewStorage(1 << 20)
	if e := backing.Write(4096, bytes.Repeat([]byte{3}, 64)); e != nil {
		t.Fatal(e)
	}
	c.State.Transactions = []transactionState{{HasRead: true, ReadAddress: 4096}}
	if n := FunctionalWritebackRange(c, 4096, 4096, backing.Write); n != 1 {
		t.Fatal("writeback range", n)
	}
	got, e := backing.Read(4096, 64)
	if e != nil {
		t.Fatal(e)
	}
	if got[0] != 3 || got[1] != 7 || got[2] != 7 || got[3] != 3 {
		t.Fatal("dirty mask was not honored")
	}
	if b.IsDirty || !b.IsValid || !b.IsLocked || b.ReadCount != 2 || !other.IsDirty {
		t.Fatal("writeback changed ownership or unrelated line")
	}
	if n := FunctionalInvalidateRange(c, 4096, 4096); n != 1 {
		t.Fatal("invalidation range", n)
	}
	if b.IsValid || !b.IsLocked || b.ReadCount != 2 || !other.IsValid || len(c.State.Transactions) != 1 {
		t.Fatal("invalidation disturbed inflight work")
	}
	if control.RetrieveOutgoing() != nil || c.GetPortByName("Bottom").RetrieveOutgoing() != nil {
		t.Fatal("functional operation queued timed work")
	}
}
