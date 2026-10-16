package localmapping

import (
	"fmt"
	"sync"
	"testing"

	"github.com/sarchlab/akita/v5/mem/vm"
)

func requirePage(t *testing.T, table vm.PageTable, expected vm.Page, addr uint64) {
	t.Helper()
	got, found := table.Find(expected.PID, addr)
	if !found || got != expected {
		t.Fatalf("wrong mapping at %#x: got %+v (found %t), want %+v", addr, got, found, expected)
	}
}

func TestOrdinaryMappingsFollowAllocationRemapAndFree(t *testing.T) {
	for _, log2 := range []uint64{12, 21} {
		t.Run(fmt.Sprint(log2), func(t *testing.T) {
			tables := NewDemandLocal(log2, 2)
			page := vm.Page{PID: 1, VAddr: 1 << log2, PAddr: 4 << log2,
				PageSize: 1 << log2, Valid: true, DeviceID: 1}
			tables.Insert(page)
			if tables.View(0).table == tables.View(1).table || tables.View(0).table == tables.master {
				t.Fatal("GPUs must use independent page tables")
			}
			for i := 0; i < 2; i++ {
				requirePage(t, tables.View(i), page, page.VAddr+127)
			}

			// The same VA in another process must not alias the first process.
			other := page
			other.PID = 2
			other.PAddr = 8 << log2
			tables.Insert(other)
			page.PAddr = 12 << log2
			page.DeviceID = 2
			tables.Update(page)
			for i := 0; i < 2; i++ {
				requirePage(t, tables.View(i), page, page.VAddr)
				requirePage(t, tables.View(i), other, other.VAddr)
				if _, found := tables.View(i).ReverseLookup(4 << log2); found {
					t.Fatal("old physical mapping survived remap")
				}
			}
			tables.Remove(page.PID, page.VAddr)
			for i := 0; i < 2; i++ {
				if _, found := tables.View(i).Find(page.PID, page.VAddr); found {
					t.Fatal("freed mapping survived")
				}
				stats := tables.View(i).Snapshot()
				if stats.Hits != 3 || stats.Missing != 1 || stats.Lookups != 4 {
					t.Fatalf("unexpected counters: %+v", stats)
				}
			}
		})
	}
}

func TestInvalidAndMigratingMappingsAreCounted(t *testing.T) {
	tables := NewDemandLocal(12, 2)
	page := vm.Page{PID: 1, VAddr: 4096, PAddr: 8192, PageSize: 4096}
	tables.Insert(page)
	tables.View(0).Find(1, 4096)
	page.Valid = true
	page.IsMigrating = true
	tables.Update(page)
	tables.View(1).Find(1, 4096)
	if tables.View(0).Snapshot().Invalid != 1 || tables.View(1).Snapshot().Migrating != 1 {
		t.Fatal("invalid and migrating outcomes were not counted separately")
	}
}

func TestLocalLookupCannotFallbackToMaster(t *testing.T) {
	tables := NewDemandLocal(12, 1)
	page := vm.Page{PID: 1, VAddr: 4096, PAddr: 8192, PageSize: 4096, Valid: true}
	tables.Insert(page)
	// Deliberately corrupt only the replica to check that Find does not hide
	// a missing local mapping by consulting the authoritative driver table.
	tables.View(0).table.Remove(1, 4096)
	if _, found := tables.View(0).Find(1, 4096); found {
		t.Fatal("local lookup fell back to the master table")
	}
	if _, found := tables.Find(1, 4096); !found {
		t.Fatal("master mapping was removed")
	}
}

func TestSharedPreservesOriginalLookupSemantics(t *testing.T) {
	tables := NewShared(12)
	page := vm.Page{PID: 1, VAddr: 4096, PAddr: 8192, PageSize: 4096}
	tables.Insert(page)
	tables.Find(1, 4096)
	if tables.View(0).Snapshot().Lookups != 0 {
		t.Fatal("driver lookup counted as MMU lookup")
	}
	got, found := tables.View(0).Find(1, 4096)
	if !found || got != page {
		t.Fatal("shared mode changed invalid-page behavior")
	}
	if _, found := tables.View(0).Find(1, 8192); found {
		t.Fatal("unallocated address was mapped")
	}
}

func TestConcurrentUpdatesAndLookups(t *testing.T) {
	tables := NewDemandLocal(12, 2)
	page := vm.Page{PID: 1, VAddr: 4096, PAddr: 8192, PageSize: 4096, Valid: true}
	tables.Insert(page)
	var wg sync.WaitGroup
	for index := 0; index < 2; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				tables.View(index).Find(1, 4096)
				tables.View(index).Snapshot()
			}
		}()
	}
	for n := 0; n < 100; n++ {
		page.PAddr = uint64(n+2) * 4096
		tables.Update(page)
	}
	wg.Wait()
	for index := 0; index < 2; index++ {
		got, _ := tables.View(index).Find(1, 4096)
		if got != page {
			t.Fatal("final mapping was not propagated")
		}
	}
}
