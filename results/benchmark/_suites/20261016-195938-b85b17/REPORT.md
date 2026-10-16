# 本轮实验结果

wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。
平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。

|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|
|---|---|---|---|---:|---:|---:|---:|
|MM|policy|inv|OK|30.5910|28.1768|1.0156|1.0000|
|MM|policy|retain|OK|30.5910|28.1768|1.1159|1.0000|
|MM|policy|i1-adaptive|OK|30.5910|28.1768|0.9655|1.0000|
|C2D|policy|inv|OK|388.4520|310.0474|16.7516|1.0000|
|C2D|policy|retain|OK|388.4520|310.0474|17.5028|1.0000|
|C2D|policy|i1-adaptive|OK|388.4520|310.0474|18.7506|1.0000|
|ST|policy|inv|OK|67.3390|63.1932|1.3690|1.0000|
|ST|policy|retain|OK|67.3390|63.1932|1.3161|1.0000|
|ST|policy|i1-adaptive|OK|67.3390|63.1932|1.4161|1.0000|
|FIR|policy|inv|OK|17.4560|15.0692|1.5173|1.0000|
|FIR|policy|retain|OK|17.4560|15.0692|1.4659|1.0000|
|FIR|policy|i1-adaptive|OK|17.4560|15.0692|1.4161|1.0000|
|IM2COL|policy|inv|OK|17.8220|14.8629|0.9688|1.0000|
|IM2COL|policy|retain|OK|17.8220|14.8629|0.9665|1.0000|
|IM2COL|policy|i1-adaptive|OK|17.8220|14.8629|0.9691|1.0000|
|BFS|policy|inv|PROCESS_FAILED|||8.0390||
|BFS|policy|retain|PROCESS_FAILED|||8.0910||
|BFS|policy|i1-adaptive|PROCESS_FAILED|||8.1425||
|SC|policy|inv|OK|17.2460|14.6562|3.6198|1.0000|
|SC|policy|retain|OK|17.2460|14.6562|3.3693|1.0000|
|SC|policy|i1-adaptive|OK|17.2460|14.6562|3.4697|1.0000|
|MT|policy|inv|OK|29.4650|15.9937|1.9676|1.0000|
|MT|policy|retain|OK|29.4650|15.9937|1.9169|1.0000|
|MT|policy|i1-adaptive|OK|29.4650|15.9937|1.9170|1.0000|
|FFT|policy|inv|OK|48.2520|46.8160|1.4661|1.0000|
|FFT|policy|retain|OK|48.2520|46.8160|1.4663|1.0000|
|FFT|policy|i1-adaptive|OK|48.2520|46.8160|1.5172|1.0000|
|PR|policy|inv|OK|35.0100|30.8754|1.8667|1.0000|
|PR|policy|retain|OK|35.0100|30.8754|1.8176|1.0000|
|PR|policy|i1-adaptive|OK|35.0100|30.8754|1.8170|1.0000|
|BS|policy|inv|OK|753.7500|720.8260|9.8842|1.0000|
|BS|policy|retain|OK|753.7500|720.8260|9.6310|1.0000|
|BS|policy|i1-adaptive|OK|753.7500|720.8260|9.6378|1.0000|
|KM|policy|inv|OK|63.5600|54.3852|2.4678|1.0000|
|KM|policy|retain|OK|63.5200|54.3849|2.3741|1.0006|
|KM|policy|i1-adaptive|OK|63.5200|54.3849|2.3735|1.0006|

完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。
opportunities 是同一个 INV shadow 的机制机会；不是每种策略的实测性能，也不表示 kernel 内被动复制协议。
