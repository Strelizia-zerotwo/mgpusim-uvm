# 本轮实验结果

wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。
平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。

|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|
|---|---|---|---|---:|---:|---:|---:|
|ST|policy|inv|OK|3432.6410|3428.5420|88.9846|1.0000|
|ST|policy|retain|OK|3447.1800|3443.0840|80.5457|0.9958|
|ST|policy|i1-adaptive|OK|9067.4940|9063.4260|239.7022|0.3786|

完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。
kernel-internal-online-v1 按运行中的实际访存触发，kernel_us 已包含 kernel 内的缓存控制和复制等待。
仅历史 kernel-barrier 结果的 opportunities 属于阶段 shadow；不能与在线协议结果混合计算收益。

|应用|策略|kernel 内 refill|kernel 内 repair|修复后再次写失效|写期间干净副本读|adaptive 按需/全补|
|---|---|---:|---:|---:|---:|---:|
|ST|inv|0|0|207|0|0/0|
|ST|retain|0|0|207|0|0/0|
|ST|i1-adaptive|0|0|207|30|0/414|
