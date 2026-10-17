# 本轮实验结果

wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。
平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。

|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|
|---|---|---|---|---:|---:|---:|---:|
|KM|policy|inv|OK|102.9450|97.0210|3.3703|1.0000|
|KM|policy|retain|OK|98.2900|92.3660|2.9196|1.0474|
|KM|policy|i1-adaptive|OK|98.2900|92.3660|2.9191|1.0474|

完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。
kernel-internal-lazy-v2 按运行中的实际访存触发，kernel_us 已包含 kernel 内的缓存控制和复制等待。
仅历史 kernel-barrier 结果的 opportunities 属于阶段 shadow；不能与在线协议结果混合计算收益。

|应用|策略|kernel 内 refill|kernel 内 repair|修复后再次写元数据|写期间干净副本读|adaptive 按需/全补|
|---|---|---:|---:|---:|---:|---:|
|KM|inv|53|0|0|0|0/0|
|KM|retain|0|54|0|0|0/0|
|KM|i1-adaptive|0|54|0|0|54/3|

缓存控制、home fetch、复制服务为并发任务耗时之和，已包含在 kernel_us 中，不可重复相加。
|应用|策略|写转换|过期子块放行|副本维护次数|副本维护字节|并发任务峰值|缓存控制 µs|
|---|---|---:|---:|---:|---:|---:|---:|
|KM|inv|0|0|83|339968|4|0.249|
|KM|retain|0|0|30|110976|4|0.144|
|KM|i1-adaptive|0|0|30|110976|4|0.144|
