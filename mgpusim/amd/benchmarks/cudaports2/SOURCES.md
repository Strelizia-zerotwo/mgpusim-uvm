# In-place/grouped variants, 2026-10-23

This independent package preserves the original cudaports package. It uses the
same pinned CUDA sources and licenses recorded in the outer project's
benchmark/mybenchmark/cuda-ports/SOURCES.json. See cuda-ports/V2.md for commands
and the exact implementation boundary.

SSSP/CC use unique vertex writers and publish every improvement in place. They
do not reproduce the source's edge-parallel atomicTestMin implementation.
COLOR preserves the same-round color guard and reads/writes one color array.
MIS preserves separate mark/drop kernels on a common state array. Both still
dispatch all vertices with inactive early exits instead of compact frontiers.
GPU workgroup progress reductions, barriers and host transfers are timed.

Embedding groups IDs using a timed GPU stable bitonic sort before per-run SGD
reduction/update. The input and objective match v1. Grouping is limited to 256
global items. It is an extracted operator, not the complete FBGEMM framework.
