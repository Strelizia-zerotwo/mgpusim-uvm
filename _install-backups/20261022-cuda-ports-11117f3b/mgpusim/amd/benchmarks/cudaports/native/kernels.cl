// Research adaptations of the CUDA algorithms cited in SOURCES.md.
// Each vertex/embedding element has one writer. Global atomics are not used.
// The fixed workgroup size matches the Go launch code.
typedef unsigned int u32;
#define GID (__builtin_amdgcn_workgroup_id_x() * 256u + __builtin_amdgcn_workitem_id_x())
#define INF 0x3fffffffu

// mode: 0 SSSP, 1 CC, 2 two-colour priority colouring, 3 priority MIS.
__attribute__((reqd_work_group_size(256, 1, 1)))
__kernel void graph_gather(__global const u32 *row, __global const u32 *col,
                           __global const u32 *weight, __global const u32 *state,
                           __global const u32 *priority, __global u32 *proposal,
                           u32 n, u32 mode, u32 iteration) {
    u32 v = GID;
    if (v >= n) return;
    u32 old = state[v];
    if (mode <= 1u) {
        u32 best = old;
        for (u32 e = row[v]; e < row[v + 1u]; ++e) {
            u32 candidate = state[col[e]];
            if (mode == 0u) candidate = candidate < INF ? candidate + weight[e] : INF;
            if (candidate < best) best = candidate;
        }
        proposal[v] = best;
    } else {
        u32 inactive = mode == 2u ? (old != INF) : (old != 0u);
        if (inactive) { proposal[v] = INF; return; }
        u32 hi = 1u, lo = 1u;
        u32 pv = priority[v];
        for (u32 e = row[v]; e < row[v + 1u]; ++e) {
            u32 u = col[e], su = state[u];
            if ((mode == 2u && su != INF) || (mode == 3u && su != 0u)) continue;
            u32 pu = priority[u];
            if (pu > pv || (pu == pv && u > v)) hi = 0u;
            if (pu < pv || (pu == pv && u < v)) lo = 0u;
        }
        if (mode == 2u) proposal[v] = hi ? 2u * iteration : (lo ? 2u * iteration + 1u : INF);
        else proposal[v] = hi ? 1u : INF;
    }
}

__attribute__((reqd_work_group_size(256, 1, 1)))
__kernel void graph_publish(__global const u32 *row, __global const u32 *col,
                            __global u32 *state, __global const u32 *proposal,
                            __global u32 *changed, u32 n, u32 mode) {
    u32 v = GID;
    if (v >= n) return;
    u32 old = state[v], next = old;
    if (mode <= 1u) next = proposal[v];
    else if (mode == 2u) {
        if (old == INF && proposal[v] != INF) next = proposal[v];
    } else if (old == 0u) {
        if (proposal[v] == 1u) next = 1u;
        else {
            for (u32 e = row[v]; e < row[v + 1u]; ++e)
                if (proposal[col[e]] == 1u) { next = 2u; break; }
        }
    }
    // Keep sparse publication, but do account for the dense progress buffer.
    if (next != old) state[v] = next;
    changed[v] = next != old;
}

// A minibatch of single-ID embedding lookups and squared-error SGD.
// Duplicate IDs are reduced by their first occurrence, on the GPU.
__attribute__((reqd_work_group_size(256, 1, 1)))
__kernel void embedding_lookup(__global const float *table, __global const u32 *ids,
                               __global float *output, u32 dim, u32 log_dim,
                               u32 items, u32 offset) {
    u32 tid = GID, sample = tid >> log_dim, d = tid & (dim - 1u);
    if (sample < items) output[tid] = table[ids[offset + sample] * dim + d];
}

__attribute__((reqd_work_group_size(256, 1, 1)))
__kernel void embedding_update(__global float *table, __global const u32 *ids,
                               __global const float *output, u32 dim, u32 log_dim,
                               u32 items, u32 offset, float step) {
    u32 tid = GID, sample = tid >> log_dim, d = tid & (dim - 1u);
    if (sample >= items) return;
    u32 id = ids[offset + sample];
    for (u32 j = 0; j < sample; ++j) if (ids[offset + j] == id) return;
    float gradient = 0.0f;
    for (u32 j = sample; j < items; ++j)
        if (ids[offset + j] == id) gradient += output[j * dim + d] - 0.125f;
    u32 p = id * dim + d;
    table[p] = table[p] - step * gradient;
}
