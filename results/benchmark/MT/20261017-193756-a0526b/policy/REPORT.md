# 本轮实验结果

wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。
平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。

|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|
|---|---|---|---|---:|---:|---:|---:|
|MT|policy|inv|OK|81.1390|78.8870|5.8252|1.0000|
|MT|policy|retain|OK|81.1390|78.8870|6.0259|1.0000|
|MT|policy|i1-adaptive|OK|81.1390|78.8870|5.9285|1.0000|

完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。
kernel-internal-lazy-v2 按运行中的实际访存触发，kernel_us 已包含 kernel 内的缓存控制和复制等待。
仅历史 kernel-barrier 结果的 opportunities 属于阶段 shadow；不能与在线协议结果混合计算收益。

|应用|策略|kernel 内 refill|kernel 内 repair|修复后再次写元数据|写期间干净副本读|adaptive 按需/全补|
|---|---|---:|---:|---:|---:|---:|
|MT|inv|0|0|0|0|0/0|
|MT|retain|0|0|0|0|0/0|
|MT|i1-adaptive|0|0|0|0|0/0|

缓存控制、home fetch、复制服务为并发任务耗时之和，已包含在 kernel_us 中，不可重复相加。
|应用|策略|写转换|过期子块放行|副本维护次数|副本维护字节|并发任务峰值|缓存控制 µs|
|---|---|---:|---:|---:|---:|---:|---:|
|MT|inv|0|0|192|786432|4|0.576|
|MT|retain|0|0|192|786432|4|0.576|
|MT|i1-adaptive|0|0|192|786432|4|0.576|
