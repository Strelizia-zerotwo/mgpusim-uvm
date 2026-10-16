# Patched Akita v5.0.0-beta.10

This directory is Akita `v5.0.0-beta.10` (copied from the Go module cache;
`examples/` and `doc-site/` omitted). `external/mgpusim/go.mod` uses it through:

```
replace github.com/sarchlab/akita/v5 => ../akita-patched
```

`address-mapper.patch` is the complete diff against the unmodified release
(apply with `patch -p1` on a fresh copy of the module to reproduce this
directory).

## The bug

In beta.10, `writethroughcache` and `writeback` snapshot their address mapper
into `Spec` at build time but only keep the module list and interleaving size.
For an `InterleavedAddressPortMapper` they drop `UseAddressSpaceLimitation`,
`LowAddress`, `HighAddress` and `ModuleForOtherAddresses`.

MGPUSim's L1V and L1S caches use such a mapper: addresses inside this GPU's
memory go to the local L2 banks, everything else to the RDMA engine. Because
the range check was dropped, L1V/L1S misses on another GPU's memory were sent
to the local L2 and local DRAM controller, which read the global storage
directly. Effects:

- no inter-GPU latency or RDMA traffic for remote data;
- each GPU's L2 caches other GPUs' data, so the L2s are incoherent (e.g.
  bitonicsort fails verification on 2+ GPUs).

Only L1I was unaffected (its address translator calls `Find()` on the mapper).

Upstream Akita fixed this on `main` in commit 7df211cc ("mem: route through
Resources address mappers instead of State", 2026-09-30), which is not in any
release yet and comes with a large API change.

## The change

Both caches now also store the range and the port for other addresses in
`Spec`, and `findPort` sends out-of-range addresses to that port first, matching
`InterleavedAddressPortMapper.Find`. `addressmapper_test.go` in each package
checks that `findPort` agrees with `Find`.

To go back to stock Akita, delete the `replace` line in `external/mgpusim/go.mod`.
