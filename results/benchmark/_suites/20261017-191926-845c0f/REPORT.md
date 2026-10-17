# 本轮实验结果

wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。
平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。

|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|
|---|---|---|---|---:|---:|---:|---:|
|C2D|policy|inv|VALIDATION_FAILED|||18.8506||
|C2D|policy|retain|VALIDATION_FAILED|||19.5027||
|C2D|policy|i1-adaptive|VALIDATION_FAILED|||18.6046||
|ST|policy|inv|VALIDATION_FAILED|||1.4701||
|ST|policy|retain|VALIDATION_FAILED|||1.4283||
|ST|policy|i1-adaptive|VALIDATION_FAILED|||1.3264||
|BFS|policy|inv|VALIDATION_FAILED|||30.9953||
|BFS|policy|retain|PROCESS_FAILED|||29.5454||
|BFS|policy|i1-adaptive|PROCESS_FAILED|||24.0244||
|PR|policy|inv|VALIDATION_FAILED|||10.1546||
|PR|policy|retain|VALIDATION_FAILED|||7.5794||
|PR|policy|i1-adaptive|VALIDATION_FAILED|||7.7792||
|BS|policy|inv|VALIDATION_FAILED|||10.7402||
|BS|policy|retain|PROCESS_FAILED|||9.1830||
|BS|policy|i1-adaptive|PROCESS_FAILED|||8.9810||

完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。
kernel-internal-lazy-v2 按运行中的实际访存触发，kernel_us 已包含 kernel 内的缓存控制和复制等待。
仅历史 kernel-barrier 结果的 opportunities 属于阶段 shadow；不能与在线协议结果混合计算收益。

|应用|策略|kernel 内 refill|kernel 内 repair|修复后再次写失效|写期间干净副本读|adaptive 按需/全补|
|---|---|---:|---:|---:|---:|---:|
|C2D|inv|||||/|
|C2D|retain|||||/|
|C2D|i1-adaptive|||||/|
|ST|inv|||||/|
|ST|retain|||||/|
|ST|i1-adaptive|||||/|
|BFS|inv|||||/|
|BFS|retain|||||/|
|BFS|i1-adaptive|||||/|
|PR|inv|||||/|
|PR|retain|||||/|
|PR|i1-adaptive|||||/|
|BS|inv|||||/|
|BS|retain|||||/|
|BS|i1-adaptive|||||/|
