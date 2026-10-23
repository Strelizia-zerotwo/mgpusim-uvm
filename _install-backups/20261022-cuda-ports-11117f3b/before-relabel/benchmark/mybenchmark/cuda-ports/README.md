# CUDA 来源研究移植版：SSSP、CC、COLOR、MIS、EMBEDDING

2026-10-22。这五个负载原本来自 CUDA 图计算或训练算子项目；现有独立 AMD/OpenCL 内核、MGPUSim Go benchmark 和 sample。它们加入 catalog 的 E 类，不改变14项论文核心集。默认只比较 `inv,infinity`，协议仍为 `kernel-internal-ideal-v3`：失效与修复在每个真实请求进入 AT 时触发。

这些是为了当前模拟器整理的**研究移植版**，不声称完整复现 Galois、Gunrock、FBGEMM 框架及其原始吞吐。已生成 AMD gfx942 二进制；自动选 CDNA3/MI300X，不需要 NVIDIA 显卡或 CUDA 驱动。

## 运行

```bash
cd /home/only/projects/mgpusim-uvm

# 只查看配置，不构建或启动应用。
python3 runs/run_cuda_ports.py --dry-run

# 5应用 × inv/infinity，共10次模拟，依次执行；默认4KiB页。
python3 runs/run_cuda_ports.py

# 先选两个图算法，或单独跑跨领域算子。
python3 runs/run_cuda_ports.py --bench SSSP,CC
python3 runs/run_cuda_ports.py --bench COLOR,MIS
python3 runs/run_cuda_ports.py --bench EMBEDDING

# 同样输入比较64KiB页，或一次列出两个页大小。
python3 runs/run_cuda_ports.py --page-bytes 65536
python3 runs/run_cuda_ports.py --page-bytes 4096,65536

# 可选：更小输入（由用户自行运行）。
python3 runs/run_cuda_ports.py --profile smoke

# 每个策略主机超时设为48小时；默认24小时。
python3 runs/run_cuda_ports.py --bench SSSP --timeout 172800
```

默认四GPU、fault=900ns、link=200ns、300GB/s，每配置一次。脚本按应用、页大小、策略顺序执行，建议只启动一套。`--timeout` 是每次策略模拟的宿主预算，构建另计。首次运行自动构建 sample；预编译内核已包含在源码包中，正常跑实验无需重新编译 AMD 内核。

## 起步输入和数值校验

|ID|sample|默认 small|更小 smoke|完成要求|
|---|---|---|---|---|
|SSSP|cuda_sssp|16384节点，目标平均度8，1个连通分量，权重1–16|1024节点，目标平均度4|收敛到固定点；所有顶点距离对比CPU Dijkstra|
|CC|cuda_cc|16384节点，目标平均度8，4个断开分量|1024节点，目标平均度4，4个分量|收敛到固定点；对比CPU连通分量最小ID标签|
|COLOR|cuda_color|16384节点，目标平均度8|1024节点，目标平均度4|所有顶点已着色，每条边两端颜色不同|
|MIS|cuda_mis|16384节点，目标平均度8|1024节点，目标平均度4|检查独立性及极大性；不是最大独立集|
|EMBEDDING|cuda_embedding|8192行×32维FP32，全局128索引/批，8批|1024行×16维，全局64索引/批，2批|多批次更新后的整张表对比CPU稀疏SGD|

图输入由固定seed=42生成，无向、无自环；每个请求的分量内先加一条链保证连通，再加随机边，去重后转为CSR。实际有向边数写入 `workload.json`，degree只是生成目标，不保证每个顶点等度。这些是实际图算法运行在生成图上，不是论文真实数据集的复现。

Embedding每个样本查一个ID，使用相同目标0.125的平方误差梯度，按全局batch平均，SGD学习率0.1。默认hotset：80%的索引来自随机置换后约1/64的表行，其余为全表均匀访问；热门行不全部塞在表头。它是单ID稀疏训练算子，包含重复ID和跨批次复用；不是完整推荐模型或EmbeddingBag实现。

用于输入敏感性的两个额外配置：

```bash
# 同样Embedding规模，改为均匀索引，比较热点影响。
python3 runs/run_cuda_ports.py --bench EMBEDDING \
  --input-config benchmark/mybenchmark/presets/cuda-embedding-uniform.json

# 大直径网格图，4096节点；轮次可能更多，先检查配置。
python3 runs/run_cuda_ports.py --bench SSSP,CC,COLOR,MIS \
  --input-config benchmark/mybenchmark/presets/cuda-grid.json --dry-run
```

输入配置会随套件保存，并进入输入哈希。各策略使用完全相同输入。64KiB页时16384个4B顶点状态恰好只有一页，静态home放置会较集中；需要结合ownership报告解读，不将页大小变化当成只改变传输字节。

## 从CUDA实现改了什么

|负载|公开来源|本次保留和调整|
|---|---|---|
|SSSP|[Galois pull SSSP](https://github.com/IntelligentSoftwareSystems/Galois/blob/b67f94206a8c47fd414446621f6633a31c49fd98/lonestar/analytics/distributed/sssp/sssp_pull_cuda.cu)|保留读取邻居距离、仅距离改善才发布；改为同步pull gather及单顶点写者发布，替代边并行atomicMin、CUDA运行时和负载均衡|
|CC|[Galois pull CC](https://github.com/IntelligentSoftwareSystems/Galois/blob/b67f94206a8c47fd414446621f6633a31c49fd98/lonestar/analytics/distributed/connected-components/cc_pull_cuda.cu)|保留较小组件标签传播和条件发布；同样采用无原子的gather/publish；当前输入无向，因此计算CC/WCC，不是强连通分量|
|COLOR|[Gunrock coloring](https://github.com/gunrock/gunrock/blob/748f79e66cc26762cd3f99422b028e742a737bde/include/gunrock/algorithms/color.hxx)|保留固定优先级极大/极小点每轮最多发布两色；改为全顶点扫描和两阶段发布，替代框架frontier/filter及随机数库|
|MIS|[LonestarGPU MIS](https://github.com/IntelligentSoftwareSystems/Galois/blob/b67f94206a8c47fd414446621f6633a31c49fd98/lonestar/analytics/gpu/independentset/mis.cu)|保留优先级胜出、标记及排除邻居；采用全顶点扫描、提案数组与单顶点发布，替代CUDA worklist|
|EMBEDDING|[FBGEMM embedding backward](https://github.com/pytorch/FBGEMM/blob/700362d243db4be4500abcdded71642de18983f8/fbgemm_gpu/codegen/training/backward/embedding_backward_split_template.cu)|抽取查表、重复ID梯度归并、按行稀疏更新结构；实现FP32平方误差SGD；重复行由GPU首次出现的位置归并，其余线程不重复写；未移植量化、Adagrad、排序缓存和PyTorch|

图算法在gather中只读当前状态，随后publish只写本顶点；提案及进度数组的稠密写、每轮进度拷回CPU均计入模拟。CPU只检查收敛和校验结果，不计算GPU迭代更新。算法阶段间的kernel提交不改变复制协议：不会在kernel边界执行预先计算的修复。

所有GPU参与同一次统一workgroup划分，普通写仍走home；没有把四GPU整轮依次串行执行。不同策略的算法、输入、kernel顺序相同。未为了产生稀疏写而移除辅助数组的实际开销；这些额外扫描也可能使收益变小，必须由实验判定。

## 输出与验证边界

目录沿用 `results/benchmark/<ID>/<suite>/policy/`，总报告在 `results/benchmark/_suites/<suite>/REPORT.md`。包含原始日志、MGPUSim指标、stats、复制/修复次数及字节、应用时间和相对INV节省、峰值保留帧、四GPU活跃情况及过期读取校验。另有：

- `workload.json`：真实轮次、节点/边数或表形状、分布、seed、完成状态。
- 图算法逐轮 `changed` 日志；Embedding逐批进度日志。
- JSON/TSV/CSV汇总包含 `workload_*` 和 `input_distribution` 字段。
- 固定版本来源、原始文件快照、SHA256及许可证保存在本目录；[SOURCES.json](SOURCES.json)是机器可读清单。

本次验证包括编译后AMD内核直接功能测试、CPU/结构性校验、统一GPU队列绑定、sample构建、脚本测试和dry-run。未启动整应用timing、smoke或性能实验，尚未证明这5项在4GPU上完整通过，也未证明会有收益。实际运行必须通过CPU结果和ideal-v3校验；失败或未收敛记录不会作为有效收益。

## 重新编译内核（仅修改kernels.cl后需要）

```bash
python3 runs/build_cuda_ports.py
```

构建脚本使用LLVM 18的AMDGPU后端。当前项目私有工具在 `_toolchains/llvm18/`，该目录已单独忽略，不提交大型编译器文件。也可提供 `--clang /path/to/clang-18 --lld /path/to/ld.lld-18`。目标gfx942、code object v4，关闭FP收缩以方便CPU数值对照；编译器版本、源码及二进制SHA256写入 `mgpusim/amd/benchmarks/cudaports/kernel-build.json`，并随实验记录源码指纹。
