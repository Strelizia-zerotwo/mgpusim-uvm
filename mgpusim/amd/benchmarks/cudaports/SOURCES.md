# CUDA-derived research workloads

These are standalone AMD/OpenCL + Go adaptations for MGPUSim, not unchanged
executions of the original CUDA frameworks. Source snapshots, pinned commits,
SHA256 hashes and licenses are in `benchmark/mybenchmark/cuda-ports/` at the
outer project root. See that directory's README for the full execution boundary.

- SSSP and CC: Galois pull algorithms, commit
  `b67f94206a8c47fd414446621f6633a31c49fd98`.
- MIS: Galois/Lonestar GPU priority-based maximal independent set, same commit.
- COLOR: Gunrock priority extrema two-colour-per-round algorithm, commit
  `748f79e66cc26762cd3f99422b028e742a737bde`.
- EMBEDDING: FBGEMM embedding lookup, backward row reduction and sparse update
  operator structure, commit `700362d243db4be4500abcdded71642de18983f8`.
  This workload implements FP32 single-ID lookup and squared-error SGD. It is
  not the complete FBGEMM implementation, DLRM, or a PyTorch training pipeline.

Global atomics and framework-specific CUDA libraries are replaced by GPU
gather/reduction and a unique writer per published vertex/parameter element.
Graph gather reads an immutable current state; a separate publish kernel writes
changed entries. Dense proposal/progress writes and host convergence transfers
are retained. The replica protocol itself still acts on every actual request
inside the kernels and does not replay or repair at kernel boundaries.

Embedding duplicate-index gradients are combined on the GPU by the first
occurrence of each row in a minibatch. Indices are generated once on the host;
the host never computes gradients or updates the table during the GPU run.

All policies execute the same algorithm, input, kernel schedule and ordinary
loads/stores. Benefits require actual measurement; compilation and functional
kernel unit tests do not establish four-GPU timing completion or speedup.
