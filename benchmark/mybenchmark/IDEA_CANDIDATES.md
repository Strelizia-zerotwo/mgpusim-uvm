# 五个补充候选：位置、输入与理想策略脚本

2026-10-17。以下源码和 sample 在当前 `/home/only/projects/mgpusim-uvm/mgpusim` 中均已存在，使用各自随仓库提供的 `kernels_gfx942.hsaco`。本轮复用已有 kernel，接到当前 `kernel-internal-ideal-v3` 请求级理想实验；2026-10-21起默认只比较 inv、infinity；delta、delta-sub可显式选择。

## 源码位置与建议起点

下面路径均相对于 `/home/only/projects/mgpusim-uvm/mgpusim/`。

|应用|计算源码|可执行 sample|small 参数|主要 GPU 数组的有效载荷|
|---|---|---|---|---:|
|lud|`amd/benchmarks/rodinia/lud/`|`amd/samples/rodinia_lud/`|`-size=128`|A：64 KiB|
|gaussian|`amd/benchmarks/rodinia/gaussian/`|`amd/samples/rodinia_gaussian/`|`-size=64`|A+M+b：32.25 KiB|
|pathfinder|`amd/benchmarks/rodinia/pathfinder/`|`amd/samples/rodinia_pathfinder/`|`-rows=16 -cols=1024`|wall+两个行缓冲：72 KiB|
|backprop|`amd/benchmarks/rodinia/backprop/`|`amd/samples/rodinia_backprop/`|`-input=256 -hidden=64 -output=4`|W1+W2：65 KiB；另有输入和中间向量|
|gramschmidt|`amd/benchmarks/polybench/gramschmidt/`|`amd/samples/polybench_gramschmidt/`|`-m=1024 -n=16`|A+Q+R+nrm：129 KiB+4 B|

这些是按实际内核划分选的小输入，不是论文原规模。字节数不含页取整、kernel 参数和副本。全部需要 `-arch=cdna3 -gpu=mi300x`；脚本自动加入。原有 GCN3/R9Nano 应用继续使用它们各自的架构。

五个 host 驱动原来在构造 benchmark 时就创建 GPU1 队列，后续 SelectGPU 不会重新绑定队列。本轮把创建移到选择目标设备之后，使统一四卡设备确实用于 kernel 提交；不改 kernel 算法或 ABI。绑定与 gfx942 内核加载由不执行 GPU 工作的单元测试覆盖。

原来的 pathfinder small=64×512 只有两个 256-thread workgroup；gramschmidt 的 M=128、N=64 也使每个 kernel 只有一个 workgroup。新输入中，pathfinder 每行四个 workgroup，gramschmidt 的 normalize 每列四个 workgroup。LUD/gaussian/backprop 的主要并行 kernel 也有足够 workgroup。四卡要求是整次应用中四块 GPU 都实际退休 CU 指令，不要求其串行步骤也使用四卡；单 workgroup 的对角分解、范数和小向量步骤保留原算法。

## 直接运行

```bash
cd /home/only/projects/mgpusim-uvm
export PATH=/usr/local/go/bin:/home/only/go/bin:$PATH

# 一次运行五个候选，small 输入，4KB 页、900ns fault、200ns link。
python3 runs/run_idea_candidates.py --timeout 1800

# 先选其中两个。
python3 runs/run_idea_candidates.py --bench lud,gaussian --timeout 1800

# 仅列出配置；不构建、不启动模拟器。
python3 runs/run_idea_candidates.py --dry-run

# 完整扫描：每应用 12 组参数 × 2 策略 = 24 次模拟。
python3 runs/run_idea_candidates.py --scan --timeout 1800
```

`--scan` 展开为 4KB/64KB/2MB 页、900/20000ns fault、0/200ns link。其他参数转交现有统一入口，可覆盖页大小、次数、超时和输入档位。

也可直接使用统一脚本，得到相同实验：

```bash
python3 runs/run_benchmarks.py run \
  --mode policy --protocol kernel-internal \
  --bench lud,gaussian,pathfinder,backprop,gramschmidt \
  --profile small --policies inv,infinity \
  --page-bytes 4096 --fault-ns 900 --link-ns 200 --timeout 1800
```

五个候选是额外的 C 类，不改变原来的 14 个核心名录。`--bench core` 仍选核心集。请用上述五个名字或专用脚本；直接选择 `--bench C` 还会包含尚未接入的其他补充应用。

## 实验和结果含义

复制、失效、修复均由 kernel 内每个实际 AT 请求触发；没有未来 trace、预取或 kernel 边界修复选择。复用三策略的免费一致性假设、home 放置和事件计费。显式页大小覆盖 MI300X 的默认 2MB 页；平台、驱动、MMU、AT、TLB 和策略配置使用同一页大小。2MB 配这些小输入会产生明显页取整和粗粒度 home 放置，应查看实际页分布。

MI300X 还有内存侧 MALL，主机 DMA 也经过它。理想功能写回绕过真实消息路径后，会免费作废对应旧 MALL 行，避免主机校验读到旧内容；不改变复制的版本和触发规则。

|应用|现有算法边界|kernel 数量（small，静态源码推导）|值得检查的事件|
|---|---|---:|---|
|lud|不选主元的分块 LU，16×16 block|22|同一矩阵的局部更新后重读|
|gaussian|GPU 前向消元；校验时 CPU 回代|126|下方区域逐渐缩小时的失效和重读|
|pathfinder|两个独立行缓冲 ping-pong，每步整行输出|15|整行覆盖写可能使 delta 节省很少；4KB 下每个行缓冲恰好一页|
|backprop|一次前向、反向和权重更新；没有训练步循环|6|权重更新之后没有下一轮前向，可能缺少写后重读|
|gramschmidt|范数步骤单线程归约，归一化写 Q 的一列，投影更新 A 的剩余列|47|跨步写与剩余列更新产生的实际过期子块；当前不建模传输区间合并|

表中的 kernel 数不是实测值，运行后以 stats.json 的 kernel_count 为准。全部有现成数值校验：LUD 检查 A≈LU；gaussian 检查线性系统残差；pathfinder 与 CPU DP 对照；backprop 比较更新后权重；gramschmidt 对照 CPU Q/R 并检查前几列正交性。

结果保存在 `results/benchmark/<应用>/<本轮ID>/policy/`，总报告在 `results/benchmark/_suites/<本轮ID>/REPORT.md`。原始日志、命令、SQLite、stats 和 JSON/TSV/CSV 汇总沿用原目录结构，含应用/kernel/宿主时间、相对 INV 节省、复制/修复次数和字节、失效以及保留帧峰值。arch/gpu 字段记录平台，并只在相同架构、页大小与服务参数内匹配 INV。

本轮按之前要求只做构建与测试，不运行 smoke 或这五个应用，因此接入不代表已完成应用数值验证或测得收益。运行时四卡活动、输入/home 一致、数值标记和理想协议不变量都会检查；失败与超时不会算作成功收益。
