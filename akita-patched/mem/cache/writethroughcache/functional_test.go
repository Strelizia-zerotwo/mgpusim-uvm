package writethroughcache

import "testing"

func TestFunctionalInvalidateHasNoControlOrDrain(t *testing.T) {
	c, _, control := rangeTestCache()
	b := &c.State.DirectoryState.Sets[0].Blocks[0]
	other := &c.State.DirectoryState.Sets[1].Blocks[0]
	b.Tag = 4096
	b.IsValid = true
	b.IsLocked = true
	b.ReadCount = 2
	other.Tag = 8192
	other.IsValid = true
	c.State.Transactions = []transactionState{{HasRead: true, ReadAddress: 4096}}
	if n := FunctionalInvalidateRange(c, 4096, 4096); n != 1 {
		t.Fatal("invalidation range", n)
	}
	if b.IsValid || !b.IsLocked || b.ReadCount != 2 || !other.IsValid || len(c.State.Transactions) != 1 {
		t.Fatal("functional invalidation changed busy ownership")
	}
	if control.RetrieveOutgoing() != nil {
		t.Fatal("functional operation emitted message")
	}
}
