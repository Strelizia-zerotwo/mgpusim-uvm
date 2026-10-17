# 运行 kernel 内理想收益实验

当前默认仅 `inv,delta,delta-sub`，协议为 `kernel-internal-ideal-v3`。旧 INV 的语义复用，但其成本也改为免费一致性，因此旧 INV 数字不能作为本轮基线。三策略需一同重跑。机制见 [KERNEL_INTERNAL.md](KERNEL_INTERNAL.md)，每个应用的小输入见 [SIZES.md](SIZES.md)。

在 Ubuntu / WSL 的仓库根目录执行：

```bash
cd /home/only/projects/mgpusim-uvm
export PATH=/usr/local/go/bin:/home/only/go/bin:$PATH

# 用现有 small 档输入先跑一个应用；脚本会构建可执行文件。
python3 runs/run_benchmarks.py run \
  --mode policy --bench BFS --profile small \
  --policies inv,delta,delta-sub --page-bytes 4096 \
  --fault-ns 900 --link-ns 200 --timeout 1800

# 核心集：14 个名录，默认运行 12 个，跳过 LeNet/VGG16。
python3 runs/run_benchmarks.py run \
  --mode policy --bench core --profile small --timeout 1800
```

不需要先 record，不读离线未来 trace。原来的 `--mode policy --bench ... --profile small` 命令仍可用，默认策略已经更换；旧命令若显式写了 retain 或 i1-adaptive，需改成三个新名字。

完整扫描在一条命令里完成；每个应用 3 页大小 × 2 fault × 2 link × 3 策略，共 36 次模拟：

```bash
python3 runs/run_benchmarks.py run \
  --mode policy --bench core --profile small \
  --page-bytes 4096,65536,2097152 \
  --fault-ns 900,20000 --link-ns 0,200 \
  --policies inv,delta,delta-sub --timeout 1800
```

可先把 `--bench core` 换成 `BFS,PR,KM` 缩短第一轮。只扫 64KB 页用 `--page-bytes 65536`。同一轮每个应用只构建一次二进制，各页大小用相同输入参数；不同页大小重新按相同规则分配 home，每种页大小内部三策略放置完全一致。

2MB 页配 small 输入可能每个分配只有少量页，页面放置会粗化，许多分配集中于某块 GPU。这是输入/页大小组合的属性；先看真实 home 分布、四卡活动和复制事件，再决定逐项放大输入，不能把它当论文的大规模配置复现。

输出位置：

```text
results/benchmark/_suites/<时间-ID>/
  manifest.json                 本轮配置和应用目录
  source-sha256.json             源码指纹
  summary.json/tsv/csv           全应用关键数字
  REPORT.md                     可读汇总
  failures.json / skipped.json  失败与显式跳过项
results/benchmark/<应用>/<时间-ID>/policy/
  summary.json/tsv/csv / REPORT.md
  <策略>_page-<字节>_link-.../
    run.log                     原始 MGPUSim 日志
    run.log.command.json/sh     实际运行命令
    run.log.resources.txt       宿主耗时/内存
    stats.json                  理想协议计数和事件
    metrics.sqlite3 / metrics.tsv
    observed-accesses.json      事实记录，不参与决策
    result.json                 该次运行状态与摘要
```

先确认 status=OK、四卡活动、数值校验、零 mapping fault、零 stale_read_sectors，以及零缓存控制/边界开销。然后比较应用时间、copy_bytes、copy_faults、refill/repair 与保留帧。收益与页大小、fault 和链路参数分别匹配同组 INV；不跨页大小或协议计算加速比。

`application_us` 是模拟时间，`wall_s` 是电脑实际耗时。复制服务累计延迟可并发，不能再加到 application_us。若三策略相同，查看 cold/refill/repair 和干净子块免修复次数；相同数字本身不证明功能未触发。

`infinity` 只在需要额外参考时显式加入 `--policies inv,delta,delta-sub,infinity`。当前默认不输出 oracle-envelope。

只汇总已有结果、不重新运行：

```bash
python3 runs/run_benchmarks.py summarize \
  --suite results/benchmark/_suites/<时间-ID>
```

LeNet 与 VGG16 默认跳过；需要时显式加 `--include-slow-dnn`。small 配置分别为 LeNet batch=2、两批，VGG16 batch=1、两批；VGG16 仍可能昂贵。`--timeout` 按每次模拟限制，超时不计收益。

本轮按要求没有运行 smoke 或任何应用实验；当前只提供通过测试的实现和脚本。`validate_kernel_internal.py` 是可选的手动微基准运行器，会启动应用，不属于本轮测试步骤。
