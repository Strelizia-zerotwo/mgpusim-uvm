# 本轮实验结果

wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。
平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。

|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|
|---|---|---|---|---:|---:|---:|---:|
|FIR|policy|inv|OK|18.4880|15.0812|16.6179|1.0000|
|FIR|policy|retain|OK|18.4880|15.0812|17.2139|1.0000|
|FIR|policy|i1-adaptive|OK|18.4880|15.0812|17.6173|1.0000|
|IM2COL|policy|inv|OK|22.9370|17.3456|10.4648|1.0000|
|IM2COL|policy|retain|OK|22.9370|17.3456|10.7011|1.0000|
|IM2COL|policy|i1-adaptive|OK|22.9370|17.3456|10.4035|1.0000|

完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。
opportunities 是同一个 INV shadow 的机制机会；不是每种策略的实测性能，也不表示 kernel 内被动复制协议。
