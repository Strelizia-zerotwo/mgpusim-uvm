# 本轮实验结果

wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。
平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。

|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|
|---|---|---|---|---:|---:|---:|---:|
|BS|policy|inv|OK|1156.7320|1123.8080|10.0372|1.0000|
|BS|policy|retain|OK|1118.5950|1085.6710|9.3326|1.0341|
|BS|policy|i1-adaptive|OK|1118.5910|1085.6670|9.0818|1.0341|

完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。
kernel-internal-lazy-v2 按运行中的实际访存触发，kernel_us 已包含 kernel 内的缓存控制和复制等待。
仅历史 kernel-barrier 结果的 opportunities 属于阶段 shadow；不能与在线协议结果混合计算收益。

|应用|策略|kernel 内 refill|kernel 内 repair|修复后再次写元数据|写期间干净副本读|adaptive 按需/全补|
|---|---|---:|---:|---:|---:|---:|
|BS|inv|165|0|67|0|0/0|
|BS|retain|0|165|67|0|0/0|
|BS|i1-adaptive|0|165|67|0|165/162|

缓存控制、home fetch、复制服务为并发任务耗时之和，已包含在 kernel_us 中，不可重复相加。
|应用|策略|写转换|过期子块放行|副本维护次数|副本维护字节|并发任务峰值|缓存控制 µs|
|---|---|---:|---:|---:|---:|---:|---:|
|BS|inv|0|0|330|1351680|1|0.99|
|BS|retain|0|0|165|664576|1|0.66|
|BS|i1-adaptive|0|0|165|664576|1|0.66|
