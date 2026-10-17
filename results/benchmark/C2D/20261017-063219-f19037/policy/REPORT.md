# 本轮实验结果

wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。
平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。

|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|
|---|---|---|---|---:|---:|---:|---:|
|C2D|policy|inv|OK|4318.0280|4239.5780|251.2860|1.0000|
|C2D|policy|retain|OK|4277.4790|4199.0280|253.4356|1.0095|
|C2D|policy|i1-adaptive|OK|6754.1220|6675.6720|327.5231|0.6393|

完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。
kernel-internal-online-v1 按运行中的实际访存触发，kernel_us 已包含 kernel 内的缓存控制和复制等待。
仅历史 kernel-barrier 结果的 opportunities 属于阶段 shadow；不能与在线协议结果混合计算收益。

|应用|策略|kernel 内 refill|kernel 内 repair|修复后再次写失效|写期间干净副本读|adaptive 按需/全补|
|---|---|---:|---:|---:|---:|---:|
|C2D|inv|330|0|309|0|0/0|
|C2D|retain|0|327|306|0|0/0|
|C2D|i1-adaptive|0|932|133|1169|836/96|
