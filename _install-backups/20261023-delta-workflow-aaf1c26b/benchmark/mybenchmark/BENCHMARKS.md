> 当前实验以 **kernel 内理想收益** 为准：2026-10-21起默认只跑 `inv,infinity`。失效、元数据、缓存与在途正确性处理免费；INV 的复制收 fault 与总字节传输，infinity 的复制/修复免费。规则和命令见 [KERNEL_INTERNAL.md](KERNEL_INTERNAL.md)、[RUNNING.md](RUNNING.md)。
>
> 核心名录仍为 14 项，默认运行 12 项；LeNet/VGG16 跳过。本轮只构建与测试，未执行应用。下面的论文列表、输入参考继续保留；有关 kernel 边界机会、旧策略和旧验证的表述属于历史口径。

# 失效副本恢复：benchmark 列表

2026-10-22：新增 E 类 CUDA 来源研究移植版 SSSP、CC、COLOR、MIS、EMBEDDING，单独入口 `runs/run_cuda_ports.py`，默认只跑 inv/infinity。它们不加入14项核心集。来源、算法改动和验证边界见 [cuda-ports/README.md](cuda-ports/README.md)。

补充更新：lud、gaussian、pathfinder、backprop、gramschmidt 已接入当前理想策略脚本。源码、规模与当前实现的算法边界见 [IDEA_CANDIDATES.md](IDEA_CANDIDATES.md)。下面 C 类的收益角色属于候选假设。

依据：CDFD（ISCA'26，13 个）、LIBRA（ISCA'26，23 个）、OASIS（HPCA'25，11 个）、GRIT（HPCA'24，8 个）、
ShadowUpdate（ISCA'26，14 个）的 benchmark 表，以及本仓库 MGPUSim 自带的 `amd/samples`。
五篇都用 MGPUSim、4 GPU，benchmark 来自 AMDAPPSDK / Hetero-Mark / SHOC / DNN-Mark。

## 1. 论文覆盖情况

| 缩写 | 应用 | CDFD | LIBRA | OASIS | GRIT | Shadow | 论文给的访问模式 | MGPUSim sample |
|---|---|:-:|:-:|:-:|:-:|:-:|---|---|
| MM | Matrix Multiplication | ✓ | ✓ | ✓ | ✓ | ✓ | Scatter-Gather | matrixmultiplication |
| C2D | Convolution 2D | ✓ | ✓ | ✓ | ✓ | ✓ | Adjacent | conv2d |
| ST | Stencil2D | ✓ | ✓ | ✓ | ✓ | | Adjacent | stencil2d |
| FIR | Finite Impulse Response | ✓ | ✓ | | ✓ | ✓ | Adjacent | fir |
| IM2COL | Image to Column | ✓ | ✓ | ✓ | | ✓ | Scatter-Gather | im2col |
| BFS | Breadth-First Search | | ✓ | ✓ | ✓ | ✓ | Random | bfs |
| SC | Simple Convolution | ✓ | ✓ | | ✓ | | Adjacent | simpleconvolution |
| MT | Matrix Transpose | ✓ | ✓ | ✓ | | | Scatter-Gather | matrixtranspose |
| FFT | Fast Fourier Transform | ✓ | ✓ | ✓ | | | Scatter-Gather | fft |
| PR | PageRank | ✓ | ✓ | ✓ | | | Random | pagerank |
| BS | Bitonic Sort | | ✓ | | ✓ | ✓ | Random | bitonicsort |
| LeNet | LeNet 训练 | | ✓ | ✓ | | ✓ | Mixed/Adjacent | lenet |
| VGG16 | VGG16 训练 | | ✓ | ✓ | | | Mixed/Adjacent | vgg16 |
| KM | K-Means | | ✓ | | | ✓ | Random | kmeans |
| SPMV | Sparse MV | | ✓ | | | ✓ | Random | spmv |
| FW | Floyd-Warshall | | | | | ✓ | — | floydwarshall |
| AES | AES 加密 | | | | | ✓ | — | aes |
| MVT | Matrix-Vector Transpose | | | | | ✓ | — | polybench_mvt（仅 gfx942） |
| MM3 | 3MM | | | | | ✓ | — | polybench_3mm（仅 gfx942） |
| GEMM | GEMM | | | | ✓ | | Scatter-Gather | polybench_gemm（仅 gfx942） |
| MIS / SSSP | 图算法 | | ✓ | | | ✓(MIS) | Random | 原仓库无；新增研究版 `cuda_mis` / `cuda_sssp`，不等同论文作者实现 |
| BERT / GPT-2 / ResNet18 | Transformer / CNN 训练 | ✓ | ✓ | ✓(ResNet) | | | Mixed | **无**（作者自己移植，未公开） |

GRIT 第 IV-A 节给出的直接动机数据：在页面复制方案下，**BS、C2D、ST 分别有 46%、49%、45% 的页面经历"写坍缩后又被读取重新复制"**。
这正是 idea 1 要优化的事件。注意它是在**被动协议、kernel 内部**测得的，不是 kernel 边界。

## 2. 建议列表

角色说明：
- **核心**：与论文对齐，必须报告
- **机会**：预计存在"失效后重读且只改少量子块"的情况，用来展示收益
- **反例**：预计没有收益，用来界定适用范围，同样必须报告
- **对照**：只读或单次写的场景，用来检查分析器（这类应用 RETAIN 与 INV 应几乎没有差别）

### A. 论文核心集（14 个，必须跑）

| 缩写 | sample | 类型 | 对 idea 1 的作用 |
|---|---|---|---|
| MM | matrixmultiplication | 稠密线代，A/B 只读共享，C 只写 | **对照**：读共享多、写不重读 |
| C2D | conv2d（加 `-enable-backward`） | DNN 层，相邻共享 | **核心**：GRIT 报告 49% 重复制；正反向两个阶段可能形成跨 kernel 重读 |
| ST | stencil2d | 迭代 stencil，halo 相邻共享 | **核心 + 反例**：每轮整块覆盖写，预计 delta 无用（初测：dirty 约 100%） |
| FIR | fir | 流式滤波，相邻共享 | 核心；单个大 kernel 时跨 kernel 机会很少 |
| IM2COL | im2col | 数据重排，scatter-gather | 核心；输出写一次，偏对照 |
| BFS | bfs | 图遍历，frontier 迭代 | **核心 + 机会**：每轮只更新部分顶点的 level，可能出现局部修改 |
| SC | simpleconvolution | 相邻共享卷积 | 核心；单 kernel，偏对照 |
| MT | matrixtranspose | 全交换 | **对照**：输出写一次 |
| FFT | fft | 多 pass，scatter-gather | 核心；pass 之间原地或乒乓写 |
| PR | pagerank | 迭代、随机访问 | **核心 + 机会**：rank 向量每轮被所有 GPU 读、按分区写 |
| BS | bitonicsort | 多 pass、交换 | **核心**：GRIT 报告 46% 重复制；初测一次修复平均合并约 5 个写阶段，但 dirty 接近整页 |
| LeNet | lenet | DNN 训练（前向、反向、更新） | **机会**：权重被所有 GPU 读、每步更新，是典型的"写后重读" |
| VGG16 | vgg16 | DNN 训练，权重更大 | 同上，footprint 更大，能检验 64KB/2MB 页 |
| KM | kmeans | 迭代聚类 | **机会**：centroid 小而共享；初测 64KB 页补齐只需约 3.6% |

### B. 论文中出现过的补充项（3～5 个）

| 缩写 | sample | 类型 | 作用 |
|---|---|---|---|
| SPMV | spmv | 稀疏、随机 | 向量读共享、结果写一次；偏对照 |
| FW | floydwarshall | 迭代 DP，原地更新 | **机会**：初测补齐字节约为整页复制的 9～14%，目前信号最强 |
| AES | aes | 流式加密 | **对照**：无共享写 |
| MVT、MM3、GEMM | polybench_*（加 `-arch cdna3 -gpu mi300x`） | 稠密线代 | 与 ShadowUpdate/GRIT 对齐；多为对照 |

### C. 为 idea 1 额外加入（论文里没有，用来界定适用范围）

| sample | 类型 | 预计结果 | 作用 |
|---|---|---|---|
| rodinia_lud | 原地分解，活跃区逐步缩小 | 页内只有部分子块被改 | **机会** |
| rodinia_gaussian | 逐行消元 | 每轮只改下方区域 | **机会** |
| rodinia_pathfinder | 逐行 DP | 每轮写一行 | **机会**（取决于行和页的对齐） |
| rodinia_backprop | 小网络训练 | 权重小更新 | **机会** |
| polybench_gramschmidt | 逐列正交化 | 每轮改一列，跨步写 | 机会；**检验分散脏块的传输合并** |
| rodinia_hotspot / rodinia_srad / polybench_jacobi2d | 双缓冲迭代 stencil | 整页覆盖写 | **反例**，与 ST 互相印证 |
| nbody | 全对全读，位置每步全部更新 | 整页覆盖写 | **反例**（8 次迭代验证失败，需用通过的规模） |

rodinia、polybench 都需要加 `-arch cdna3 -gpu mi300x`。

### D. 合成微基准（建议自己写）

在分析器端直接生成 trace，不需要模拟器。参数包括：写入页内比例 f、脏块分布（连续/分散）、读者回来的阶段数 k、读者集合大小。
这样可以画出 RETAIN 相对 INV、GPS 的收益曲面，并在受控条件下验证机制；真实应用只负责证明这种机会确实存在。

### 不纳入

- MIS、SSSP、BERT、GPT-2、ResNet18：本 MGPUSim 中没有，也找不到作者的移植版本
- atax、bicg：上轮只在 GPU1 有访问，需先排查
- nw：单 GPU 也验证失败
- 各类 microbench：与 UVM 共享无关

## 3. 跑之前必须注意

1. **kernel 数量决定这些应用在第一阶段有没有意义。** 第一阶段只在显式全局阶段边界修复。若某应用实际只有单阶段，则跨阶段恢复机会为零。SC、MT、MM、FIR、IM2COL、AES 的具体 kernel 数必须实测；C2D 底层是复合算子，不能把一次前向直接当成单 kernel。
   GRIT 测到的 45～49% 来自 kernel 内部的写坍缩，要覆盖这一部分，分析器还需要一个"被动协议"模式：首次写即坍缩，下次读即修复。但 kernel 内部恢复副本，就会遇到"修复后被后续写悄悄变旧"的问题。
   风险：如果收益只出现在多 kernel 应用里，而论文核心集大多是单 kernel，审稿人一定会问。
   每个应用跑完先看 `trace.meta` 里 `K` 行的数量。上面关于单 kernel 的判断来自读源码，未逐个确认。
2. **GPU 划分。** GRIT 写明 TB 调度方式为"先填满一块 GPU，再填下一块"，这对应 MGPUSim 的 unified multi-GPU。当前 `-unified-gpus=1,2,3,4` 是静态连续 workgroup 分区；和 GRIT 的容量驱动填满再切卡描述有相似局部性，但尚未证明调度等价，不能直接写一致。
3. **输入规模。** LIBRA 的每 GPU footprint 为 4～544MB（14 核心项为 6～55MB），CDFD 明确按每 GPU 列出 384MB～8.7GB；GRIT 的 16～155MB 是应用 footprint 表，不能统称每 GPU。分析 4KB 页时几十 MB 就够；64KB 页至少需要上百 MB；2MB 页需要 GB 级，用 emulation 才跑得动。
4. **目标是同时报告两种 dirty 定义；当前仅实现写请求掩码。** 一种是写掩码（现实中可行的硬件脏位），另一种加 `-changed`，只算值真正改变的字节（绝对下界）。两者差距大，说明静默写很多，这本身就是一个值得写进论文的发现。
5. **判断机会是否足够时，每个应用至少看这几项：**
   - 失效后被重读的比例（refill / invalidations）
   - 重读时 dirty 子块比例的分布（p50 和直方图）
   - 一次修复合并的写阶段数
   - 重读距离（隔了几个阶段）
   - 旧副本占用的显存（stale retained 的峰值相对 footprint）


2026-10-19 推荐规模入口已增加：`python3 runs/run_recommended.py`，默认第一档BFS/FW/gaussian/lud，可选择第二档gramschmidt/C2D/NW和目标规模。完整参数与NW验证边界见 [RECOMMENDED.md](RECOMMENDED.md)。

2026-10-21：复制免费参考入口 `python3 runs/run_infinity.py`，默认 BFS/NW/lud/FW × inv/infinity；教程见 [INFINITY.md](INFINITY.md)。


2026-10-23：新增独立 E 类 `SSSP-2、CC-2、COLOR-2、MIS-2、EMBEDDING-2`，保留旧五项。新版本恢复原地状态读写或改用计时 GPU 分组；它们仍是明确标注的研究移植版，不是完整 CUDA 框架。入口 `python3 runs/run_cuda_ports2.py`，详见 [cuda-ports/V2.md](cuda-ports/V2.md)。
