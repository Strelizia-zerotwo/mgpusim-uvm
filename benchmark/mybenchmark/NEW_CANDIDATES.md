# 第二批新增候选：接近 BFS 的稀疏更新与重读

核对日期：2026-10-23。本表不重复前十个候选：SSSP、CC、图着色、MIS、Embedding、MF-SGD、k-core、BC、动态 Katz、MST。

后续接入状态（2026-10-23）：七项已提供独立算法级 AMD 适配及四策略入口，见 [运行说明](irregular/README.md) 和 [算法/访存差异](irregular/FIDELITY.md)。LP、DMR 等存在显式算法或调度替代，不能把下面的上游描述当作适配版的逐条访存说明。本表保留初始筛选分析，没有本轮性能测量。优先级综合考虑差量复制机会、领域覆盖和移植成本，不能解读为预期加速比。公开 CUDA 或 HIP 实现不能直接作为 MGPUSim 可执行程序。

|优先级|新增 benchmark|源码确认与可能机会|限制与移植成本|
|---|---|---|---|
|1|SCC：强连通分量|GARDENIA 的 Forward–Backward–Trim 实现明确调用前向、后向可达性搜索；base.cu 的 bfs_step 条件更新 visited/expanded。不同搜索轮反复读取共享状态，模式最接近现有 BFS。|需要有向图及反向 CSR；清零、颜色更新、trim 都可能产生密集写，须纳入计时。中等移植成本。|
|2|残差驱动个性化 PageRank / PPR|Gunrock ppr.hxx 从 seed 激活 frontier，传播 r/r_prime，越过度数相关 epsilon 阈值才激活后继。状态可能在同页内稀疏改变后反复重读。|当前 main 实现每轮 thrust::copy_n 整个 r_prime 到 r；不能把它描述为全程稀疏写。原子累加、队列和 dense copy 的成本必须保留；若改成其他 residual 算法要单独标注。中等移植成本。|
|3|SymGS：对称 Gauss–Seidel 平滑|GARDENIA base.cu 的 gs_kernel 遍历稀疏行并读取 x[j]，每个颜色阶段仅写 x[inew]，再按颜色做前向和后向 sweep。它提供非图分析的稀疏数值计算场景。|颜色中的变量是否在物理页混合、跨 GPU 邻接是否足够决定机会；一整个 sweep 会更新全部变量。颜色预处理与数据布局需记录，不能仅凭局部阶段推断整页修改很少。中等移植成本。|
|4|Points-to Analysis：指针指向分析|LonestarGPU 明确提供 CUDA PTA 应用，输入为指针约束，计算 flow-insensitive/context-insensitive points-to 结果；源码有 diffPtsMask 等增量集合结构。数据流传播具有读取邻接集合、局部增长、迭代到不动点的机会。|尚未逐一审计设备端所有集合更新；集合可能是 bitset/链式结构，脏块密度与分配布局不同。完整 PTA 包含 load/store 约束及间接关系，简化成普通 OR 传播时必须标为子算子。移植成本较高。|
|5|Label Propagation：社区标签传播|Gunrock master 有 lp 实现，遍历邻居标签并通过分段众数更新 labels；后期标签可能稳定，仍会重读邻居。|所查代码仍有 labels[idx]=... 的普遍写回及自赋值；值不变不等于没有写指令。当前按写掩码统计时可能削弱 delta 收益。原子、排序/众数与收敛也要计入。中到高移植成本。|
|6|Louvain：模块度社区发现|Gunrock master 有 louvain 实现，计算社区增益、移动顶点，并更新社区统计；后期移动可能只涉及部分顶点。|共享统计上的原子操作可能很密集，并含图收缩/重建。只测局部移动应标为 Louvain local-moving 子算子，不能当完整 Louvain。高移植成本。|
|7|Delaunay Mesh Refinement：网格细化|LonestarGPU scientific 提供 CUDA DMR，坏三角形触发局部 cavity 重构，之后继续检查邻接结构。可形成局部结构修改与重读。|拓扑增删、动态容量、冲突协调和不确定调度使移植与校验都更难；空间分区还可能让重读几乎全在本地。高移植成本，后续再做。|

## 公开源码入口

- SCC：[GARDENIA base.cu](https://github.com/chenxuhao/gardenia/blob/master/src/scc/base.cu)、[topo.cu](https://github.com/chenxuhao/gardenia/blob/master/src/scc/topo.cu)。源码中存在 bfs_step、fw_reach/bw_reach 和 fwd_reach/bwd_reach。
- SCC 的另一个实现：[ECL-SCC 官方页面](https://userweb.cs.txstate.edu/~burtscher/research/ECL-SCC/)、[CUDA 源码](https://userweb.cs.txstate.edu/~burtscher/research/ECL-SCC/source/ECL-SCC_10.cu)。这是 SC 2023 的最大 ID 传播算法，不能与 Forward–Backward BFS 算法混为一谈；有工作列表原子操作和块内收敛原语，不是直接可用的 MGPUSim 程序。
- PPR：[Gunrock ppr.hxx](https://github.com/gunrock/gunrock/blob/main/include/gunrock/algorithms/ppr.hxx)。
- SymGS：[GARDENIA base.cu](https://github.com/chenxuhao/gardenia/blob/master/src/symgs/base.cu)、[Makefile](https://github.com/chenxuhao/gardenia/blob/master/src/symgs/Makefile)。
- PTA：[LonestarGPU README](https://github.com/IntelligentSoftwareSystems/Galois/blob/master/lonestar/analytics/gpu/pointstoanalysis/README.md)、[pta.cu](https://github.com/IntelligentSoftwareSystems/Galois/blob/master/lonestar/analytics/gpu/pointstoanalysis/pta.cu)。
- 社区标签传播：[Gunrock master lp_enactor.cuh](https://github.com/gunrock/gunrock/blob/master/gunrock/app/lp/lp_enactor.cuh)。
- Louvain：[Gunrock master louvain_enactor.cuh](https://github.com/gunrock/gunrock/blob/master/gunrock/app/louvain/louvain_enactor.cuh)。
- DMR：[LonestarGPU README](https://github.com/IntelligentSoftwareSystems/Galois/blob/master/lonestar/scientific/gpu/delaunayrefinement/README.md)。

以上分支链接会变化；本次读取内容保存在工作区 .inspection/benchmark-search-20261023/。它们只用于候选筛选，正式移植时仍需固定上游 commit、许可和输入来源。

## 建议的最小筛选顺序

1. SCC、PPR、SymGS；再做 PTA。先建立数值正确的小实现，再扩规模。
2. 图类初筛可考虑 16K–64K 顶点与约 4–16 平均度，均为建议输入而非已实现 CLI 参数。SCC 需要多个强连通区域和单向区域间连接；PPR 需要明确 seed、alpha、epsilon 和实际收敛条件。
3. SymGS 初筛可用 8K–32K 行的稀疏对角占优矩阵、相同颜色顺序和矩阵布局；原实现每次包含前后 sweep，若增加外层迭代须明确计数。
4. 首先用相同输入比较 inv/infinity 判断复制相关时间差；再用 inv/delta/delta-sub 确认差量机制能否实际利用机会。infinity 的收益不能直接视为 delta 的收益。
5. 同时看失效后远端重读次数、每次修复的过期 32B 子块比例、复制/修复服务次数与字节、四 GPU 活动和数值校验。

## 选择输入时的关键区别

高直径带来更多轮次，但如果道路网按空间划分后几乎全是本地边，未必有多少复制重读。随机重编号能改变页混合及 home 分布，属于实验变量，需要对所有策略使用同一映射并明确报告，不能只报告最有利重编号。

普通三角形计数、随机游走、只读稀疏查询往往主要读取固定图，不具有持续修改共享状态后重读的机会，不是本轮优先候选。一般全量 GNN 前向也常稠密重写嵌入，不能仅因为用了图就当作 BFS 型稀疏更新。
