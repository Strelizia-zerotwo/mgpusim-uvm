# 七个新增研究实现的算法与访存边界

实现标识：`irregular-algorithmic-amd-v1`。它们是独立编写的 AMD/OpenCL + Go 算法实现，参考固定提交的公开 CUDA 代码；不是将整个 CUDA 框架原样运行在 MGPUSim 上。来源、提交、文件 SHA256 和原版权说明保存在 SOURCES.json 与 upstream/。

当前版本没有声称原版访存等价，workload.json 明确记录 `reference_trace_equivalent=false`。功能校验不能证明访问序列等价。研究结论必须限定到这里的算法、调度、输入和实现，不能把某项低收益推广为原始应用的优化空间小。

## 共同实验条件

- `inv,infinity,delta,delta-sub` 复用现有 `kernel-internal-ideal-v3`；实际读写在 AT 入口触发检查，包括 TLB 命中。不增加 kernel 边界恢复，也不读取未来 trace。
- 四策略使用相同可执行文件、输入、页大小、home 放置、900ns fault 默认值、200ns link 默认值和带宽。所有一致性与元数据处理仍按已有理想协议免费。
- benchmark 源码不识别策略名，没有为某个策略改变输入或算法。
- kernel 内所需状态更新不统一改写成 proposal/publish。原算法确实需要阶段或冲突处理时，阶段保留并计时。
- 不支持的原子操作未伪装为免费硬件。下面逐项说明替代的数据访问和调度。
- GPU 工作、GPU 辅助数组、GPU 归约、控制数据 D2H/H2D 都正常计时。CPU 输入生成、CPU 校验与普通 host 控制计算不建模 CPU 周期，与当前 MGPUSim 驱动的边界一致。
- 固定 seed 的生成输入不等于真实数据集。random 图沿用已有输入生成器的随机重编号，影响页内混合和跨卡边，作为输入分布明确报告。

## SCC

参考 GARDENIA Forward–Backward–Trim。保留 `bfs_step` 的 kernel 内 push 结构：读取邻居 visited，条件写 `visited[dst]=1`；当前顶点的 expanded 只有一个写者。多个写者向同一个 visited 写相同的 1，是单调幂等更新，未引入 proposal 快照。前向和反向搜索分别沿正向和反向 CSR。

差别：控制器每次选择一个尚未归类的枢轴，重复搜索并移除交集，未复刻上游多颜色子问题的多枢轴优化；全顶点 dispatch 与工作组级终止计数替代上游全局 changed 标记/前沿结构。保持 kernel 内交错，但搜索次数、活跃集及访存量不保证一致。

数值检查使用独立 Tarjan 算法比较 SCC 分区，允许不同的 SCC 编号。

## PPR

参考 Gunrock ppr.hxx，保留 filter、advance、dense-copy 三阶段：累加 p，清除活跃顶点的 r_prime，传播残差，再整数组复制 r_prime 到 r。阈值是 degree×epsilon，迭代到无活跃节点。没有为了增强收益而删掉全量复制。

差别：advance 按目的顶点读取入边，对每个活跃来源进行逐项加法并写自己的 r_prime，替代边并行 atomicAdd。没有把已经原地传播的源实现误写成不相关的 proposal 算法，但 push/pull、原子争用和浮点加法次序仍不同。当前生成图无 sink；有 sink 的输入会拒绝，不隐含选择 dangling-node 语义。

检查 CPU 阶段参考结果、剩余残差及质量守恒。FP32 除法以受支持的倒数指令实现，并做容差校验。

## SymGS

参考 GARDENIA gs_kernel。输入为生成图对应的对称严格对角占优矩阵：非对角为 -1，对角为 degree+1。CPU 贪心着色属于输入预处理；每个颜色 kernel 原地读写同一 x 向量，先正向再反向完成一个 sweep，重复请求的 sweep 次数。

差别：每个颜色 dispatch 全体顶点并按颜色退出，未采用上游压紧的 indices 区间；输入矩阵是特定生成矩阵，不代表所有科学矩阵。没有 proposal 或旧 x 快照。

检查 CPU 同颜色顺序的完整 sweep 结果。完成指定 sweep 不等于求解器已经收敛，因此 metadata 使用 completed=true、convergence_required=false，明确记录完成标准。

## PTA

实现有限对象全集上的 flow-insensitive/context-insensitive Andersen 闭包，支持完整的四类约束：`x=&o`、`x=y`、`x=*y`、`*x=y`。对象本身也是可解引用的 pointer node；load/store 真正传播其 points-to 集，不是仅做普通 copy 图传播。

差别很大：用精确、有限长度的 bitset 替代上游动态稀疏集合、差量集合和 HCD 优化。每个 pointer row 有一个写者，读取邻居集合后原地 OR；每个 heap object row 扫描 store 约束。不会丢失集合事实，但扫描量、集合布局和访存轨迹不同，可能稀释性能收益。没有模拟真实程序前端，默认输入是生成约束。

用独立、按四类约束直接求不动点的 CPU 实现逐 bit 比较。对象数、约束分类计数和轮数写入 workload.json。

## LP

当前是**按颜色调度的原地社区标签传播**，不是 Gunrock 的同步分段众数实现。每个颜色阶段读邻居 labels 并直接写自己的 label；只有多数票严格改善时改变标签，平票保留旧标签。相同颜色没有相邻顶点，所以每次改变严格增加一致边数，有限步终止。

选择这个明确命名的算法变体，是为了有定义的收敛和 kernel 内原地状态读写；它改变了 Gunrock 的同步邻居收集/发布结构及标签轨迹。不能用它代表原版 Gunrock LP 的性能。没有隐瞒这个调度差别，也不能将它的收益和原版配对解释。

校验 CPU 同颜色顺序的独立 majority-vote 实现，要求最终标签逐顶点相同。

## Louvain

执行**完整多层流程**：计算权重度数和社区总量，提出正模块度增益的移动，选择互不共享源/目的社区的移动，应用并重算总量；局部稳定后压缩社区编号、重写全部边端点、GPU bitonic 排序、重建 GPU CSR，再在收缩图上继续。原始顶点的社区映射一直保留到最终层。保留自环与平行加权边，未把图收缩拿到 CPU 免费完成。

差别：用确定性的互斥社区仲裁代替上游 atomic 更新。仲裁和总量归约目前扫描顶点，可能为二次复杂度；社区编号压缩也有额外扫描。GPU 排序采用简单网络，不是上游高效库。这些都会影响端到端时间，均实际计费。默认规模较小，不能据此判断优化过的 Louvain 没有空间。

检查每层单顶点移动局部最优、模块度不下降、GPU 收缩前后加权邻接相同、最终原始分区与收缩层模块度一致。Louvain 是启发式，不声称全局最优分区或与上游相同分区。

## DMR

当前为**单位方形上的 Delaunay 面积细化**：GPU Bowyer–Watson 插入初始点；GPU 选择面积超限的三角形，在其重心增加新点；GPU 计算 circumcircle cavity、提取边界、扫描空槽并原地重建拓扑，直到所有最终内部三角形面积不超过 max-area。

这是有真实三角形增删和 Delaunay 性质的网格细化，不是固定图 trace 或只读几何计算。但它**不是 Lonestar 的 Chew 最小角度细化算法**，不声称改善最小角度或支持任意受约束边界。每次只插入一个点，没有上游并行 cavity 冲突处理；boundary 提取扫描三角形，吞吐和交错程度可能明显不同。

GPU 做 cavity 和拓扑，host 只选择工作组归约后的候选，并对少量工作组计数做 prefix 控制；传输都计时。为四卡提供至少 1024 个 triangle-capacity 槽，但实际活跃三角形可能很少；四卡执行过指令不等于四卡有效负载均衡。

使用 GPU FP32 几何判定、CPU FP64 校验；检查正向三角形、面积达标、总覆盖面积、无内部孔洞、边流形性、全部插入点保留和 empty-circle 性质。容量不足或几何校验失败直接拒绝结果，不伪装成功。

## 当前证据边界

已执行的功能单元测试会运行编译后的 gfx942 指令和真实控制器，另有 ELF 寄存器资源检查。它们不是四卡时序实验，也没有给出加速比。正式运行由统一脚本另外检查四卡指令活动、初始输入及 home 一致、数值验证和理想协议不变量。
