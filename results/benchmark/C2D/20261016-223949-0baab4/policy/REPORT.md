# 本轮实验结果

wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。
平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。

|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|
|---|---|---|---|---:|---:|---:|---:|
|C2D|policy|inv|OK|667.7810|601.5320|14.8423|1.0000|
|C2D|policy|retain|OK|667.5600|601.3110|15.4463|1.0003|
|C2D|policy|i1-adaptive|OK|899.2860|833.0370|18.9002|0.7426|

完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。
kernel-internal-online-v1 按运行中的实际访存触发，kernel_us 已包含 kernel 内的缓存控制和复制等待。
仅历史 kernel-barrier 结果的 opportunities 属于阶段 shadow；不能与在线协议结果混合计算收益。

|应用|策略|kernel 内 refill|kernel 内 repair|修复后再次写失效|写期间干净副本读|adaptive 按需/全补|
|---|---|---:|---:|---:|---:|---:|
|C2D|inv|23|0|14|0|0/0|
|C2D|retain|0|23|14|0|0/0|
|C2D|i1-adaptive|0|106|3|30|106/0|
