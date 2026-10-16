# 小规模 benchmark 与复制策略实验：运行教程

工作目录是 `/home/only/projects/mgpusim-uvm`；Go 模块在其下的 `mgpusim/`。
脚本放在 `runs/`，实验定义放在 `benchmark/mybenchmark/`，结果放在 `results/benchmark/`，每个 benchmark 有独立子目录。

## 先理解当前能测什么

| 模式 | 实际执行 | 能回答什么 |
|---|---|---|
| `stock` | 上游 sample，`shared` 页表、`-unified-gpus=1,2,3,4`，时序执行和 CPU 校验 | 输入能否跑通、实际 GPU 活动、普通 kernel 时间；不能表示 7 策略收益 |
| `demand` | 上游 sample，4 个独立 GPU、统一内存、映射缺页 | 映射缺页平台检查；不能作为免费预装映射的 NoRep |
| `record` | 已接入的并行 ST/FW，真实时序运行并记录访问计划 | 先看 refill、脏比例、重读距离等机会，避免立即跑全部策略 |
| `policy` | 同一并行应用先 record，再按同一个 plan 跑 7 个策略 | 真实时序复制实验；共用所有者放置、映射准备、页大小、缓存屏障和链路参数 |

目前 `policy` / `record` 只接入 **ST、FW**。其他应用请求这两个模式会留下明确的阻塞状态。
LeNet/VGG16 保留在 14 项核心清单内，但原训练 sample 不支持统一内存，`Verify()` 也尚未实现，因此当前会在预检查阶段记为阻塞。
这两个应用不能通过删除 `-verify`、删除统一内存开关，就宣称完成 UVM/复制策略实验。
完整接入状态见 [IMPLEMENTATION_STATUS.md](IMPLEMENTATION_STATUS.md)。

## 1. 准备环境和查看参数

在 Ubuntu / WSL 终端执行：

```bash
cd /home/only/projects/mgpusim-uvm
export PATH=/usr/local/go/bin:/home/only/go/bin:$PATH
python3 --version
go version
python3 runs/run_benchmarks.py audit
python3 runs/run_benchmarks.py list --bench core --profile small
```

需要 Python 3.10 以上和仓库所要求的 Go 版本；Python 脚本只使用标准库。
`/usr/bin/time` 存在时会额外保存宿主峰值内存和 CPU 时间；缺失时仍可运行，`peak_rss_kib` 留空。
`akita-patched/` 必须与 `mgpusim/` 相邻，保留 `go.mod` 的本地 replace。
不需要真实 AMD/NVIDIA GPU，也不使用 `nvcc`。

只看任务、不编译和模拟：

```bash
python3 runs/run_benchmarks.py run --mode stock --bench core --profile smoke --dry-run
python3 runs/run_benchmarks.py run --mode policy --bench ST,FW --profile small --dry-run
```

`smoke` 用于检查路径，`small` 是建议的第一轮小规模。所有参数都在 `catalog.json`，详见 [SIZES.md](SIZES.md)。
stock ST 的 row/col 指内部尺寸，而 policy ST 的 size/columns 包括 halo，两者不要互换。

## 2. 第一轮：核心 14 项的平台检查

```bash
python3 runs/run_benchmarks.py run \
  --mode stock --bench core --profile smoke --timeout 1800
```

脚本逐个编译、串行运行，避免同时启动多个模拟器造成宿主内存压力。
每项都使用 `-verify`；普通 sample 检查各自的成功标记，C2D/IM2COL 使用 runner 提前启用的逐算子 CPU 校验。
训练 sample 的阻塞会被记录，脚本继续后面的应用。
检查依据是逐 GPU 汇总的 CU 指令数，保存在 `instructions_by_gpu`；CommandProcessor 收到 kernel launch 但分不到 workgroup 时仍可能报告非零 kernel_time，不能用它判断实际活动。
如果只有 GPU1 或部分 GPU 执行指令，结果记为 `INSUFFICIENT_GPU_ACTIVITY`，不能因为命令写了 4 个 GPU 就当成四卡结果。
小输入出现这种状态时，先看 `metrics.tsv`，再增大输入，不要忽略检查。

第一轮跑通后，选定应用改成 `small`：

```bash
python3 runs/run_benchmarks.py run \
  --mode stock --bench MM,FIR,SC,MT,FFT,PR,BS,KM --profile small --timeout 3600
```

只想单独检查原来的映射缺页平台，可使用：

```bash
python3 runs/run_benchmarks.py run \
  --mode demand --bench FIR,SC,BS --profile smoke
```

`demand` 不与 `-unified-gpus` 混用。BFS/C2D/IM2COL 的普通 sample 拒绝多张独立 GPU，脚本会提前解释。
此处 `-gpus` 依赖应用自己实现分工；脚本检查四张物理 GPU 的实际 kernel 活动。

## 3. 第二轮：先记录 ST/FW 的机制机会

```bash
python3 runs/run_benchmarks.py run \
  --mode record --bench ST,FW --profile small --timeout 1800
```

每项会生成 `record/observed-plan.json`、`record/stats.json`、原始 SQLite、`opportunities.json/tsv`。
record 是采集访问计划的一遍执行，它的时间不作为正式 NoRep 回放结果。

`opportunities.json` 包含：

- `invalidations`、`refills`、`refill_per_invalidation`：无限容量 INV shadow 中的副本事件计数；末尾失效可能来不及再读，因此不是页面百分比。
- `dirty_fraction_p50`、`dirty_fraction_histogram`：refill 时，自上次复制/修复以来累计写过的 32 B 子块占页面的比例。
- `merged_write_epochs_p50`：该次 refill 合并了几个写阶段；计写阶段，不计单个写请求。
- `reread_distance_p50`：第一次使该副本失效的阶段到重读阶段之间的全局阶段数。
- `peak_stale_bytes`、`peak_stale_over_home_footprint`：每个全局阶段结束时的旧副本占用峰值；是保留全部旧副本的 INV shadow 统计，不是实际 INV 物理帧占用。
- `refill_events`：每个事件的 page/GPU/epoch、脏块数、需要读取的脏块数、合并阶段数和距离。

没有 refill 时，各分布中位数留空，不能写成“脏比例为 0，所以效果很好”。
`epochs` 是全局阶段数，不能当成 kernel launch 数。一阶段可包含 4 张 GPU 的并行 kernel。
当前通用内存 trace 扩展已不在源码内，没有 `trace.meta` 和值改变掩码；`kernel_count=null`、`changed_bytes_available=false` 会明确保留这个缺口。
当前先报告写请求掩码，尚不能报告真正 changed bytes 的下界。

## 4. 第三轮：完整 7 策略比较

```bash
python3 runs/run_benchmarks.py run \
  --mode policy --bench ST,FW --profile small \
  --policies norep,inv,retain,i1-no-s3,i1,i1-full-stale,i1-adaptive \
  --page-bytes 4096 --link-ns 200 --fault-ns 900 \
  --bandwidth-gbs 300 --repeats 1 --timeout 1800
```

以上 7 项就是默认策略，因此 `--policies` 可以省略。`retain` 和 `i1-no-s3` 已包含。
每个应用执行一次 record，再执行 7 次真实时序回放。输入、物理 home、plan、免费预装映射与缓存屏障保持相同。
脚本复用原回放验证器，额外检查实际访问掩码与事务计数完全一致、四 GPU kernel 活动、时间分解和物理所有者一致。
严格 I-1 变体还要求写阶段新复制计数为 0；映射缺页必须为 0。

固定服务和附加传播的敏感性：

```bash
python3 runs/run_benchmarks.py run \
  --mode policy --bench ST,FW --profile small \
  --link-ns 0,200 --fault-ns 900,20000 --repeats 2
```

这是每应用 `1 + 2×2×2×7 = 57` 次模拟，先完成单点再扫描。
`link-ns` 是现有 RDMA 阶段之外的附加单向延迟；300 GB/s 是模型预算，二者都不是实机测量。
额外三策略可显式选择：

```bash
python3 runs/run_benchmarks.py run --mode policy --bench ST --profile small \
  --policies norep,inv,retain,i1-no-s3,i1,i1-full-stale,i1-adaptive,i1-write-prefetch,i2,infinity
```

`i2` 默认额外预算 metadata=2 ns、shootdown=100 ns；不表示真实硬件实现。
本清单仅校验了 4 KiB 的 owner 对齐几何。64 KiB/2 MiB 请求会明确拒绝，需先建立满足页对齐和容量要求的新 profile。

## 5. 结果在哪里、怎么看

控制台会显示本轮 `SUITE` 和 `REPORT` 的绝对路径。输出结构：

```text
results/benchmark/
  latest-suite.json
  _suites/<批次>/
    REPORT.md                 # 本轮所有应用的总表
    summary.csv / summary.tsv / summary.json
    failures.json             # 阻塞、超时、数值或活动检查失败
    manifest.json             # 参数、时间、应用路径、接入状态
    source-sha256.json         # Go/OpenCL/hsaco、模块和实验配置哈希
    bin/                      # 本轮编译的二进制及哈希
    build/<sample>/run.log
  ST/<批次>/policy/
    REPORT.md / summary.*
    benchmark.json / suite.json
    opportunities.json / opportunities.tsv
    oracle-envelope.json / oracle-envelope.tsv
    validated-policy-stats.json
    record/...
    inv_link-200ns_fault-900ns_repeat-0/
      run.log                 # simulator 原始 stdout + stderr
      run.log.command.json    # argv 与 cwd
      run.log.command.sh      # 便于查看命令；重跑须在该命令记录的 cwd 中
      run.log.process.json    # exit status、宿主耗时、峰值 RSS
      run.log.resources.txt   # /usr/bin/time -v 原始统计（若可用）
      metrics.sqlite3         # MGPUSim 原始统计
      metrics.tsv             # 展开的所有 Location/What/Value/Unit
      stats.json              # 复制模型全部计数
      observed-plan.json      # 本次实际访问
      result.json             # 本次关键数据摘要
```

普通 `stock` / `demand` 的结构类似，单次运行放在 `repeat-0/`。
每次运行都使用新批次目录，保留旧结果；每个应用结束就更新总表。

关键时间口径：

| 字段 | 含义 |
|---|---|
| `wall_s` | 宿主实际等待模拟器运行的秒数，不含 build |
| `kernel_us` | policy：Session 的真实 kernel 阶段时间；stock/demand：Driver busy time，单位 µs |
| `barrier_us` | 实际缓存屏障时间 |
| `recovery_us` | 恢复等待时间，已考虑模型内的并发，不是逐事件简单求和 |
| `application_us` | `(kernel_ps + barrier_flush_ps + recovery_wait_ps)/1e6`；ps 转 µs |
| `speedup_vs_inv` | 同应用、同参数、同 repeat 的 `INV application_us / 当前 application_us`，大于 1 更快 |
| `saving_percent_vs_inv` | `100×(1 - 当前时间/INV时间)`，负值表示变慢 |

完整复制量、RDMA、DRAM、cache/TLB、主机排队等仍保留在原文件和展开表内。
失败运行不计算收益。平台检查的应用总时间、复制量和相对 INV 收益留空。
每个 repeat 单独比较，不从多个 repeat 中挑最快的来当稳定收益。
summary 中 `shadow_*` 开头的四项来自共同 record 的无限容量 INV shadow；每种策略自身的实际 `refills` / `invalidations` / `peak_stale_bytes` 则来自该次回放的 stats，二者不可互相替代。

`oracle-envelope` 从本轮实际成功的 INV/i1/i1-full-stale/i1-adaptive 整次应用中选择，记录候选和所选策略。
如果某个候选失败，候选列表会减少；若 INV 失败，该组不会生成 envelope。它是离线选择结果，不是第 11 个可执行策略。

只重新汇总已有文件，不跑模拟：

```bash
python3 runs/run_benchmarks.py summarize \
  --suite /home/only/projects/mgpusim-uvm/results/benchmark/_suites/<实际批次>
```

## 6. 补充应用和常见状态

```bash
python3 runs/run_benchmarks.py run --mode stock --bench B --profile smoke
python3 runs/run_benchmarks.py run --mode stock --bench C --profile smoke
python3 runs/run_benchmarks.py run --mode stock --bench FW,lud,gramschmidt --profile small
```

polybench/rodinia 自动带 `-arch=cdna3 -gpu=mi300x`，与 r9nano 应用分开看，不能将两种硬件的时间混成公平策略比较。
nbody 先用 1 次迭代；2 次以上的正确性仍需验证，失败不进入分析。

D 类合成记录：

```bash
python3 runs/generate_synthetic.py
```

默认扫描 dirty 比例 1/128、1/16、1/4、1，连续/分散两种分布，重读距离 1/4/16，累计写阶段 1/4，读者数 1/3。
会剔除距离短于累计写阶段数的组合。输出在 `results/benchmark/synthetic/<批次>/`，保留每组生成的 plan、owner 与五项机制机会、总表。
总表报告整页 refill 与全过期差量 refill 的有效载荷、分散区间数量和 16 B/区间的头部开销；它没有运行 MGPUSim，也没有生成 7 策略时序收益。
这些生成的 plan 仅用于离线机制分析，不能直接替换真实应用 record 的地址与访问来做策略回放。

退出码 0 表示本次所请求的全部运行通过；2 表示至少一项阻塞、失败、超时或 GPU 活动不足。
查看 `failures.json` 和该项 `run.log`，不要把退出码 2 解释成所有应用都失败。
超时可以提高 `--timeout 3600`，或者把输入从 small 改回 smoke。超时会结束整个模拟器进程组。
按 Ctrl+C 会终止当前模拟；之前完成的 `result.json` 可用 summarize 收回总表。
本版没有自动续跑；再次执行会建立新批次，需要重跑时用 `--bench` 只选相应应用。

当前脚本没有把未接入应用伪装成完成的 7 策略实验。要对全部 14 个应用做端到端策略比较，仍需给剩余应用接入真实 managed buffers、全局阶段、owner 分配与真实四 GPU 并行，并完成逐策略数值校验；训练应用还需要解决 UVM 与数据集接口。
