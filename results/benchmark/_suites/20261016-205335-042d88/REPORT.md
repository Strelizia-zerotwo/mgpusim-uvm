# 本轮实验结果

wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。
平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。

|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|
|---|---|---|---|---:|---:|---:|---:|
|MM|policy|inv|OK|89.6990|76.2277|12.8108|1.0000|
|MM|policy|retain|OK|89.6990|76.2277|13.8190|1.0000|
|MM|policy|i1-adaptive|OK|89.6990|76.2277|13.1597|1.0000|
|C2D|policy|inv|OK|1300.2040|1175.0986|114.1887|1.0000|
|C2D|policy|retain|OK|1300.2040|1175.0986|117.4373|1.0000|
|C2D|policy|i1-adaptive|OK|1300.2040|1175.0986|114.0440|1.0000|
|ST|policy|inv|OK|183.1600|130.3946|10.2945|1.0000|
|ST|policy|retain|OK|183.3750|130.6758|10.3449|0.9988|
|ST|policy|i1-adaptive|OK|183.1370|130.6749|11.2474|1.0001|

完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。
opportunities 是同一个 INV shadow 的机制机会；不是每种策略的实测性能，也不表示 kernel 内被动复制协议。
