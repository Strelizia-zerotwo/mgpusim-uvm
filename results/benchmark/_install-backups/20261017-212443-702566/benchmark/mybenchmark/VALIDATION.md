# Kernel 内按页修复 v2：最终验证

核心批次：`/home/only/projects/mgpusim-uvm/results/benchmark/_suites/20261017-195059-3feed8`。单 kernel 批次：`/home/only/projects/mgpusim-uvm/results/benchmark/_kernel-internal-validation/20261017-195031-c55252`。

12 个应用 × 3 策略 = 36 项通过；LeNet、VGG16 按用户要求跳过。所有应用使用四块物理 GPU，4 KB 页、32 B 子块、200 ns 链路、900 ns 固定服务、300 GB/s 参数带宽。
各策略的实际 home 和初始化输入 SHA 一致。整批源码哈希与当前文件逐项匹配，九项单 kernel 验证使用相同源码哈希。
数值校验、协议完成、每请求版本断言及 kernel 边界有效副本字节检查通过。

|应用|INV µs|retain µs|adaptive µs|adaptive 相对 INV 时间减少|INV kernel 内 refill|adaptive kernel 内 repair|adaptive 本地干净读|
|---|---:|---:|---:|---:|---:|---:|---:|
|MM|40.999|40.999|40.999|0.00%|0|0|0|
|C2D|639.110|639.110|639.110|0.00%|0|0|0|
|ST|69.430|69.430|69.430|0.00%|0|0|0|
|FIR|21.600|21.600|21.600|0.00%|0|0|0|
|IM2COL|23.917|23.917|23.917|0.00%|0|0|0|
|BFS|297.062|292.785|280.646|5.53%|22|7|41|
|SC|21.681|21.681|21.681|0.00%|0|0|0|
|MT|81.139|81.139|81.139|0.00%|0|0|0|
|FFT|48.252|48.252|48.252|0.00%|0|0|0|
|PR|58.723|58.723|58.723|0.00%|0|0|0|
|BS|966.259|966.259|966.259|0.00%|0|0|0|
|KM|77.726|77.684|77.684|0.05%|0|0|0|

`application_us` 为模型应用时间，包含 kernel、边界缓存处理和 host transfer。不是宿主耗时，也不是实机 GPU 测量。smoke 用于验证和小规模观察，未完成正式规模扫描。
MM/FIR/MT/FFT 等相同的结果保留；是否有收益取决于实际复制、写入、重读事件。

|应用|策略|写请求|写转换|过期子块放行|缓存控制 µs（任务之和）|home fetch µs（任务之和）|并发任务峰值|
|---|---|---:|---:|---:|---:|---:|---:|
|MM|inv|256|0|0|0.048|1.104|3|
|MM|retain|256|0|0|0.048|1.104|3|
|MM|i1-adaptive|256|0|0|0.048|1.104|3|
|C2D|inv|6657|0|0|5.109|3.970|4|
|C2D|retain|6657|0|0|5.109|3.970|4|
|C2D|i1-adaptive|6657|0|0|5.109|3.970|4|
|ST|inv|640|0|0|0.024|0.976|3|
|ST|retain|640|0|0|0.024|0.976|3|
|ST|i1-adaptive|640|0|0|0.024|0.976|3|
|FIR|inv|256|0|0|0.018|0.202|2|
|FIR|retain|256|0|0|0.018|0.202|2|
|FIR|i1-adaptive|256|0|0|0.018|0.202|2|
|IM2COL|inv|273|0|0|0.009|0.154|1|
|IM2COL|retain|273|0|0|0.009|0.154|1|
|IM2COL|i1-adaptive|273|0|0|0.009|0.154|1|
|BFS|inv|2164|0|0|4.965|4.701|4|
|BFS|retain|2160|0|0|4.965|5.374|4|
|BFS|i1-adaptive|2166|0|0|4.908|4.428|4|
|SC|inv|993|0|0|0.018|0.520|4|
|SC|retain|993|0|0|0.018|0.520|4|
|SC|i1-adaptive|993|0|0|0.018|0.520|4|
|MT|inv|4096|0|0|0.576|9.856|4|
|MT|retain|4096|0|0|0.576|9.856|4|
|MT|i1-adaptive|4096|0|0|0.576|9.856|4|
|FFT|inv|1024|0|0|0.000|0.000|0|
|FFT|retain|1024|0|0|0.000|0.000|0|
|FFT|i1-adaptive|1024|0|0|0.000|0.000|0|
|PR|inv|512|0|0|0.045|0.770|3|
|PR|retain|512|0|0|0.045|0.770|3|
|PR|i1-adaptive|512|0|0|0.045|0.770|3|
|BS|inv|5696|0|0|0.495|8.470|1|
|BS|retain|5696|0|0|0.495|8.470|1|
|BS|i1-adaptive|5696|0|0|0.495|8.470|1|
|KM|inv|640|0|0|0.090|1.540|4|
|KM|retain|640|0|0|0.090|1.537|4|
|KM|i1-adaptive|640|0|0|0.090|1.537|4|

各内部计时已包含于 kernel 时间，并发任务可以重叠，不能加到 application_us。源端通过真实 L2 请求，kernel 内 home_flush_events=0；目标显存写入为功能性直接写入，未另建目标 DRAM 写队列。元数据查找免费，未实现独立 S4 硬件。

## 单 kernel 测试

每例四轮、同一个 kernel，CPU 检查整个页。repeated-write 和 clean-local-read 的四个工作组都写；写者对写过的整页回 home。clean-reader 保留最后一个工作组只读，以验证其他 GPU 的 S3。

|用例|策略|INV refill|差量 repair|干净本地读|写转换|过期放行|
|---|---|---:|---:|---:|---:|---:|
|repeated-write|inv|0|0|0|0|0|
|repeated-write|retain|0|0|0|0|0|
|repeated-write|i1-adaptive|0|0|0|0|0|
|clean-local-read|inv|0|0|0|0|0|
|clean-local-read|retain|0|0|0|0|0|
|clean-local-read|i1-adaptive|0|0|0|0|0|
|clean-reader|inv|4|0|0|0|0|
|clean-reader|retain|0|3|0|0|0|
|clean-reader|i1-adaptive|0|0|16|0|0|

源码回归：整个模块 go build 通过；replica、driver、localmapping、runner 与缓存/AT 的 race 回归通过；Python 八项统计回归通过。另有每 GPU 一页的实际 FIR 复用测试、普通 demand、历史 kernel-barrier 和手工 system-scope 标注路径验证。

## 运行

```bash
cd /home/only/projects/mgpusim-uvm
python3 runs/validate_kernel_internal.py
python3 runs/run_benchmarks.py run --mode policy --bench core --profile smoke --policies inv,retain,i1-adaptive --timeout 600
```

详细机制、同步边界和教程见 `benchmark/mybenchmark/KERNEL_INTERNAL.md`。原始 SQLite、metrics.tsv 和全部应用输出保留在 Linux 的各应用目录。此 Windows 导出包含汇总、原始 stats、日志、命令和来源哈希。
