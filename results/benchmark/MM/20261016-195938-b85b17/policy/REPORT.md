# 本轮实验结果

wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。
平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。

|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|
|---|---|---|---|---:|---:|---:|---:|
|MM|policy|inv|OK|30.5910|28.1768|1.0156|1.0000|
|MM|policy|retain|OK|30.5910|28.1768|1.1159|1.0000|
|MM|policy|i1-adaptive|OK|30.5910|28.1768|0.9655|1.0000|

完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。
opportunities 是同一个 INV shadow 的机制机会；不是每种策略的实测性能，也不表示 kernel 内被动复制协议。
