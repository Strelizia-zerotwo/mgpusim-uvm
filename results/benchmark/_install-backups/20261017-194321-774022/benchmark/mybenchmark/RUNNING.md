# 核心应用：kernel 内在线三策略实验

当前默认入口是 `core-kernel-internal-online-v1`。INV、retain 和 i1-adaptive 都由 kernel 执行中的实际请求触发；adaptive 只使用历史信息。完整规则和时间含义见 [KERNEL_INTERNAL.md](KERNEL_INTERNAL.md)。

```bash
cd /home/only/projects/mgpusim-uvm
export PATH=/usr/local/go/bin:/home/only/go/bin:$PATH

# 先检查普通应用和排序。
python3 runs/run_benchmarks.py run \
  --mode policy --protocol kernel-internal \
  --bench FIR,BS --profile smoke --timeout 600

# 默认 12 个应用 × inv、retain、i1-adaptive。
python3 runs/run_benchmarks.py run \
  --mode policy --protocol kernel-internal \
  --bench core --profile smoke --timeout 600
```

14 项核心名录保留；LeNet、VGG16 按用户要求默认跳过，跳过不计为通过。`--policies inv,retain,i1-adaptive` 为默认值。先看 smoke 的实际计数，再选择 `--profile small`。

总报告在 `results/benchmark/_suites/<suite>/REPORT.md`；每个应用单独存放在 `results/benchmark/<应用>/<suite>/policy/`。保存配置、命令、日志、SQLite、metrics.tsv、stats.json、访问摘要、汇总 JSON/TSV/CSV。

终端和报告会显示 kernel 内 refill/repair、修复后再次写失效、写期间干净副本读、adaptive 两种选择。没有这些事件时不能仅凭 OK 认定相关机制已被评估。

在线协议的 kernel_us 已含 kernel 内缓存处理和复制等待。内部解释项不能再次加到 application_us。每种策略直接运行，不要求先 record，也不读未来计划。

历史 kernel 边界实验需显式添加 `--protocol kernel-barrier`，不能与在线结果混算收益。普通 `--mode demand` 仍为独立的映射缺页平台检查。

慢速 DNN 只有显式加 `--include-slow-dnn` 才启用；该版本尚无完整在线训练验证结论。
