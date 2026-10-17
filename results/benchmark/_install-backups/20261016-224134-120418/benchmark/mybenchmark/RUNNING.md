# 核心应用的三策略实验

工作目录：`/home/only/projects/mgpusim-uvm`，Go 模块：`mgpusim/`。
默认策略已经改为 **inv、retain、i1-adaptive**。
核心名录保留 14 项；按用户要求，**LeNet 和 VGG16 默认跳过**，主线实际运行其余 **12 项 × 3 策略**。

## 首次运行

```bash
cd /home/only/projects/mgpusim-uvm
export PATH=/usr/local/go/bin:/home/only/go/bin:$PATH
python3 runs/run_benchmarks.py audit
python3 runs/run_benchmarks.py run --mode policy --bench core --profile smoke --dry-run
python3 runs/run_benchmarks.py run --mode policy --bench core --profile smoke --timeout 1800
```

默认就是下面三项，不必手动填写：

```text
--policies inv,retain,i1-adaptive
```

每个应用先做一次真实时序 record，再分别回放三种策略，共四次模拟。LeNet/VGG16 生成 `SKIPPED_BY_REQUEST` 状态，不计入已通过应用和收益统计，也不计为失败。
所有任务串行运行，每次建立新批次目录，保留旧结果。

只运行选定应用：

```bash
python3 runs/run_benchmarks.py run --mode policy --bench BFS,PR,KM --profile smoke
python3 runs/run_benchmarks.py run --mode policy --bench MM,ST,FIR --profile small --timeout 3600
```

先筛机会、不跑三次策略回放：

```bash
python3 runs/run_benchmarks.py run --mode record --bench core --profile small
```

输入在 `catalog.json`；逐项大小见 [SIZES.md](SIZES.md)。先用 smoke 检查，再逐项放大到 small。应用输入、页大小、物理放置和模拟参数不一致的运行不能计算相互加速比。

## 现在接入的是哪一层

核心应用使用 `core-kernel-barrier-v1` adapter：保留 sample 的 kernel 与算法调用，把每次 kernel 提交转换为一个全局、排空的执行阶段。驱动将工作组按连续区间均分给四张物理 GPU，每张 GPU 保留独立 MMU、TLB、cache 和物理内存。
这是明确的 benchmark 分区适配；不是 GRIT 容量驱动调度的完全复现，也不是实机动态复制协议。

数据分配按每个缓冲区的连续页区间静态放到四张 GPU，记录真实 home。小缓冲区或页边界可能出现非 home 写，报告 `non_owner_write_transactions`，并沿用页/阶段冲突回退。
不能把这种通用放置称为原并行 ST 的“每次写都在 owner”布局。每个应用三策略使用完全相同的放置。

H2D 数据更新被记录为显式 `host-write` 阶段；释放临时张量会记录 `retire` 阶段并回收 home/副本物理帧、撤销映射和 TLB。复用物理帧前统一初始化新分配的数据页。代码、kernel 参数和 dispatch packet 使用普通映射，不计入数据副本。
普通 `-vm-mode=demand` 的含义仍是映射缺页；只有显式 `-replica-core` 才启用本 adapter。

## BFS 的边界

BFS 中多个 GPU 竞争发现同一顶点，实际执行写入的 GPU 可能随时序改变，因此不能把一次 record 的精确写掩码直接当成所有运行的固定写集。
此应用使用显式保存的 **保守访问计划**：CSR 数组为只读，levels/flag 声明可写范围并回退 home；所有策略使用同一计划。实际请求仍必须落在声明范围内，最终 levels 必须通过 CPU 检查，全部逻辑缓冲区摘要也必须一致。

`observed-plan.json` 保留实际访问，`oracle-plan.json` 是回放计划，`conservative-plan-scope.json` 说明范围。保守计划不是实际访问量，不应把声明写集当作测得的 dirty 比例。`opportunities.json` 从实际 record 分析，不能把它解释成这个保守回退模型已经获得了对应收益。
其他 11 个默认应用要求实际访问掩码、事务计数、生命周期和写出数据摘要与 record 一致。

## 结果和时间

```text
results/benchmark/
  _suites/<批次>/
    REPORT.md
    summary.csv / summary.tsv / summary.json
    failures.json / skipped.json
    manifest.json / source-sha256.json / summary-provenance.json
    bin/                          # 本轮二进制及哈希
    build/<sample>/run.log
  <应用>/<批次>/policy/
    REPORT.md / summary.* / adapter.json
    opportunities.json / opportunities.tsv
    oracle-plan.json
    oracle-envelope.json / oracle-envelope.tsv
    record/
    inv_link-200ns_fault-900ns_repeat-0/
      run.log                     # MGPUSim stdout + stderr
      run.log.command.json        # argv 和 cwd
      run.log.process.json        # 退出状态、宿主耗时、RSS
      run.log.resources.txt       # /usr/bin/time -v，若可用
      metrics.sqlite3             # 原始 MGPUSim 统计
      metrics.tsv                 # 所有原始指标展开
      stats.json / result.json
      observed-plan.json
```

关键字段：

| 字段 | 含义 |
|---|---|
| wall_s | 宿主实际运行秒数，不含 build |
| kernel_us | Session 记录的 kernel 执行时间 |
| barrier_us | 实际缓存屏障时间 |
| recovery_us | 复制/恢复的等待时间，包含模型内并发 |
| host_transfer_us | 主机写阶段、生命周期操作及 kernel 间推进的模拟时间；包括本轮数值检查产生的相关 I/O |
| application_us | kernel + barrier + recovery + host_transfer |
| speedup_vs_inv | 同应用、同配置、同 repeat 的 INV 时间 / 当前时间 |
| kernel_count | adapter 捕获的逻辑 kernel 提交次数；一次提交可分到四张物理 GPU |
| epochs | kernel、host-write、retire 的全局阶段总数，不能代替 kernel_count |
| instructions_by_gpu | 实际 CU 指令数，空 kernel launch 不计作执行活动 |

初始化在第一个 kernel 前完成；其后的模拟时间均按上述分解核对。独立 CPU 运算消耗宿主时间；只有推进了模拟时钟的 I/O 才进入模拟时间分解。正式讨论收益时也应报告共同屏障和主机处理成本占比。

三策略均免费预装映射并清理 TLB，回放 `mapping_faults=0`。核对物理 home、数值结果、真实四卡活动和时间分解；严格 adaptive 不允许在写阶段建立冷副本。
`copy_bytes`、`copy_faults`、`refills`、`invalidations`、实际副本/旧副本峰值和冲突回退都来自本次 stats。
共同 record 的机制机会以 `shadow_*` 开头，另给 dirty 直方图、合并写阶段数、重读距离与生命周期感知的旧副本占用。这些字段不能代替各策略的实际计数。没有事件时分布留空。
当前仍使用 32 B 写请求掩码，没有逐字节 changed-value 下界。

`oracle-envelope` 对本轮实际成功的 INV/retain/i1-adaptive 整次应用做离线选择，包含 INV；它不是第四种可执行策略。
失败、超时、跳过均不计算收益。退出码 0 表示所有非跳过运行通过；2 表示至少一项失败、阻塞、超时或活动不足。

只重新汇总已有结果：

```bash
python3 runs/run_benchmarks.py summarize --suite results/benchmark/_suites/<实际批次>
```

## 扫描和重复

```bash
python3 runs/run_benchmarks.py run --mode policy --bench BFS,PR,KM --profile small \
  --link-ns 0,200 --fault-ns 900,20000 --repeats 2
```

这是每应用 1 次 record + 24 次回放，先完成单点再扫描。
默认 4 KiB 页、32 B 传输粒度、300 GB/s 预算、附加单向 200 ns、固定服务 900 ns；这些参数沿用当前模型，不是硬件测量。本 catalog 暂时明确拒绝其他页大小，避免未验证的对齐比较。

## LeNet / VGG16：默认跳过，需要时显式打开

```bash
python3 runs/run_benchmarks.py run --mode policy --bench LeNet --profile smoke \
  --include-slow-dnn --timeout 1800
```

LeNet 已接入，使用相同初始 FP32 权重、输入序列、独立 CPU forward/backward/Adam 对照并检查训练参数；逐算子检查同时保留。
两种训练应用的通用 adapter 使用 **一个共享模型、每个 kernel 分到四张独立 GPU**。不是上游“四份模型 + MCCL all-reduce”的数据并行调度，不能声称完全复现论文训练划分。

默认可选自检输入是确定性合成数据，明确记录在 adapter.json，不是 MNIST/Tiny-ImageNet。smoke 仅一批；small 两批。VGG16 自检显式使用 `-input-size=32`，保留 sample 的层次与通道配置，FC 输入随空间大小改变；不能和原 224×224 输入结果混报。
按用户要求已停止 VGG16 的耗时验证；其完整 GPU 训练/回放未被标为通过。

真实数据集路径保留，例如：

```bash
python3 runs/run_benchmarks.py run --mode policy --bench LeNet --profile small \
  --include-slow-dnn --dnn-input dataset --mnist-data-folder /实际/MNIST路径
python3 runs/run_benchmarks.py run --mode policy --bench VGG16 --profile small \
  --include-slow-dnn --dnn-input dataset --imagenet-data-folder /实际/tiny-imagenet-200路径
```

真实 ImageNet 路径自动使用 224×224；输入和 footprint 会明显增加。脚本不自动下载数据集。

## 其他入口

`--mode stock`、`--mode demand` 保留普通平台检查；它们的 Driver kernel_time 不能混入三策略收益比较。
补充 FW 可用 `--mode policy --bench FW`，仍走原 `parallel-fw` adapter；其布局和阶段说明会保留，不能与通用 ST/FW 调度混称相同。
原 7 策略配置仍供历史显式 adapter 使用，核心通用入口当前只开放用户选择的三种策略。

```bash
python3 runs/generate_synthetic.py
```

合成记录只输出机制机会与有效载荷比较，不产生模拟器时序收益。
