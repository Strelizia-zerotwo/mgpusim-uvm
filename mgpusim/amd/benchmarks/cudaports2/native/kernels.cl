// Version 2: in-place graph state, and GPU grouping for sparse SGD.
// SSSP/CC deliberately use one writer per vertex. These are not edge-parallel
// atomicMin kernels: every improving value is published by that vertex's owner.
typedef unsigned int u32;
#define LID (__builtin_amdgcn_workitem_id_x())
#define WG (__builtin_amdgcn_workgroup_id_x())
#define GID (WG * 256u + LID)
#define INF 0x3fffffffu

// Every lane must reach this routine, including lanes outside the input extent.
__attribute__((always_inline)) static inline void report_count(
    u32 value, volatile __local u32 *scratch, __global u32 *counts) {
    u32 lane = LID;
    scratch[lane] = value;
    __builtin_amdgcn_s_barrier();
    for (u32 offset = 128u; offset; offset >>= 1u) {
        if (lane < offset) scratch[lane] += scratch[lane + offset];
        __builtin_amdgcn_s_barrier();
    }
    if (lane == 0u) counts[WG] = scratch[0];
}

__attribute__((reqd_work_group_size(256, 1, 1)))
__kernel void graph_inplace(__global const u32 *row, __global const u32 *col,
    __global const u32 *weight, volatile __global u32 *state,
    __global const u32 *priority, __global u32 *counts,
    u32 n, u32 mode, u32 iteration) {
    __local u32 scratch[256];
    u32 v = GID, changed = 0u;
    if (v < n) {
        u32 old = state[v];
        if (mode <= 1u) {
            u32 best = old;
            for (u32 e = row[v]; e < row[v + 1u]; ++e) {
                u32 candidate = state[col[e]];
                if (mode == 0u) {
                    u32 w = weight[e];
                    candidate = candidate < INF && w < INF - candidate ? candidate + w : INF;
                }
                if (candidate < best) {
                    best = candidate;
                    state[v] = best; // Publish each improvement, inside this kernel.
                    changed = 1u;
                }
            }
        } else if (old == INF) {
            u32 hi = 1u, lo = 1u, pv = priority[v], color = 2u * iteration;
            for (u32 e = row[v]; e < row[v + 1u]; ++e) {
                u32 u = col[e], su = state[u];
                // A neighbour colored in THIS round still participates, exactly
                // as in Gunrock. Ignoring it could color adjacent vertices alike.
                if (u == v || (su != INF && su != color && su != color + 1u)) continue;
                u32 pu = priority[u];
                if (pu > pv || (pu == pv && u > v)) hi = 0u;
                if (pu < pv || (pu == pv && u < v)) lo = 0u;
            }
            if (hi || lo) {
                state[v] = hi ? color : color + 1u;
                changed = 1u;
            }
        }
    }
    report_count(changed, scratch, counts);
}

__attribute__((reqd_work_group_size(256, 1, 1)))
__kernel void mis_mark(__global const u32 *row, __global const u32 *col,
    volatile __global u32 *state, __global const u32 *priority, u32 n) {
    u32 v = GID;
    if (v >= n || state[v] != 0u) return;
    u32 pv = priority[v], winner = 1u;
    for (u32 e = row[v]; e < row[v + 1u]; ++e) {
        u32 u = col[e];
        if (u == v || state[u] == 2u) continue;
        u32 pu = priority[u];
        if (pu > pv || (pu == pv && u > v)) { winner = 0u; break; }
    }
    if (winner) state[v] = 1u;
}

__attribute__((reqd_work_group_size(256, 1, 1)))
__kernel void mis_drop(__global const u32 *row, __global const u32 *col,
    volatile __global u32 *state, __global u32 *counts, u32 n) {
    __local u32 scratch[256];
    u32 v = GID, remaining = 0u;
    if (v < n && state[v] == 0u) {
        u32 drop = 0u;
        for (u32 e = row[v]; e < row[v + 1u]; ++e)
            if (state[col[e]] == 1u) { drop = 1u; break; }
        if (drop) state[v] = 2u;
        else remaining = 1u;
    }
    report_count(remaining, scratch, counts);
}

__attribute__((reqd_work_group_size(256, 1, 1)))
__kernel void embedding_lookup(__global const float *table, __global const u32 *ids,
    __global float *output, __global const u32 *order,
    u32 dim, u32 log_dim, u32 items, u32 offset, float step) {
    u32 tid = GID, sample = tid >> log_dim, d = tid & (dim - 1u);
    if (sample < items) output[tid] = table[ids[offset + sample] * dim + d];
}

// GPU bitonic grouping, bounded to 256 items. Stable (ID, original position)
// ordering preserves gradient addition order; no host sorting or free grouping.
__attribute__((reqd_work_group_size(256, 1, 1)))
__kernel void embedding_group(__global const u32 *ids, __global u32 *order,
    u32 items, u32 offset) {
    volatile __local u32 keys[256];
    volatile __local u32 positions[256];
    u32 lane = LID;
    keys[lane] = lane < items ? ids[offset + lane] : 0xffffffffu;
    positions[lane] = lane;
    __builtin_amdgcn_s_barrier();
    for (u32 k = 2u; k <= 256u; k <<= 1u) {
        for (u32 j = k >> 1u; j; j >>= 1u) {
            u32 other = lane ^ j;
            u32 a = keys[lane], b = keys[other];
            u32 pa = positions[lane], pb = positions[other];
            u32 greater = a > b || (a == b && pa > pb);
            u32 less = a < b || (a == b && pa < pb);
            u32 take_other = ((lane & k) == 0u) == ((lane & j) == 0u) ? greater : less;
            __builtin_amdgcn_s_barrier();
            if (take_other) { keys[lane] = b; positions[lane] = pb; }
            __builtin_amdgcn_s_barrier();
        }
    }
    if (lane < items) order[lane] = positions[lane];
}

__attribute__((reqd_work_group_size(256, 1, 1)))
__kernel void embedding_update(__global float *table, __global const u32 *ids,
    __global const float *output, __global const u32 *order,
    u32 dim, u32 log_dim, u32 items, u32 offset, float step) {
    u32 tid = GID, run = tid >> log_dim, d = tid & (dim - 1u);
    if (run >= items) return;
    u32 sample = order[run], id = ids[offset + sample];
    if (run && ids[offset + order[run - 1u]] == id) return;
    float gradient = 0.0f;
    for (u32 j = run; j < items; ++j) {
        u32 index = order[j];
        if (ids[offset + index] != id) break;
        gradient += output[index * dim + d] - 0.125f;
    }
    u32 p = id * dim + d;
    table[p] = table[p] - step * gradient;
}
