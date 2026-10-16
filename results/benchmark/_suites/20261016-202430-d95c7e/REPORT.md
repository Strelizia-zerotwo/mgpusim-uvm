# 本轮实验结果

wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。
平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。

|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|
|---|---|---|---|---:|---:|---:|---:|
|MM|policy|inv|OK|30.5910|28.1768|1.1170|1.0000|
|MM|policy|retain|OK|30.5910|28.1768|1.1170|1.0000|
|MM|policy|i1-adaptive|OK|30.5910|28.1768|1.1670|1.0000|
|C2D|policy|inv|OK|388.4520|310.0474|16.1638|1.0000|
|C2D|policy|retain|OK|388.4520|310.0474|15.8582|1.0000|
|C2D|policy|i1-adaptive|OK|388.4520|310.0474|16.1619|1.0000|
|ST|policy|inv|OK|67.3390|63.1932|1.5683|1.0000|
|ST|policy|retain|OK|67.3390|63.1932|1.5186|1.0000|
|ST|policy|i1-adaptive|OK|67.3390|63.1932|1.4687|1.0000|
|FIR|policy|inv|OK|17.4560|15.0692|1.6181|1.0000|
|FIR|policy|retain|OK|17.4560|15.0692|1.5731|1.0000|
|FIR|policy|i1-adaptive|OK|17.4560|15.0692|1.6683|1.0000|
|IM2COL|policy|inv|OK|17.8220|14.8629|1.0669|1.0000|
|IM2COL|policy|retain|OK|17.8220|14.8629|1.1175|1.0000|
|IM2COL|policy|i1-adaptive|OK|17.8220|14.8629|1.0675|1.0000|
|BFS|policy|inv|OK|143.1870|127.0312|10.3432|1.0000|
|BFS|policy|retain|OK|143.1870|127.0312|10.1935|1.0000|
|BFS|policy|i1-adaptive|OK|143.1870|127.0312|9.8908|1.0000|
|SC|policy|inv|OK|17.2460|14.6562|3.6235|1.0000|
|SC|policy|retain|OK|17.2460|14.6562|3.5763|1.0000|
|SC|policy|i1-adaptive|OK|17.2460|14.6562|3.6732|1.0000|
|MT|policy|inv|OK|29.4650|15.9937|2.0695|1.0000|
|MT|policy|retain|OK|29.4650|15.9937|2.1196|1.0000|
|MT|policy|i1-adaptive|OK|29.4650|15.9937|2.1697|1.0000|
|FFT|policy|inv|OK|48.2520|46.8160|1.7184|1.0000|
|FFT|policy|retain|OK|48.2520|46.8160|1.7687|1.0000|
|FFT|policy|i1-adaptive|OK|48.2520|46.8160|1.6688|1.0000|
|PR|policy|inv|OK|35.0100|30.8754|2.1199|1.0000|
|PR|policy|retain|OK|35.0100|30.8754|2.0195|1.0000|
|PR|policy|i1-adaptive|OK|35.0100|30.8754|2.0192|1.0000|
|BS|policy|inv|OK|753.7500|720.8260|10.8958|1.0000|
|BS|policy|retain|OK|753.7500|720.8260|10.5430|1.0000|
|BS|policy|i1-adaptive|OK|753.7500|720.8260|10.5928|1.0000|
|LeNet|policy||SKIPPED_BY_REQUEST|||||
|VGG16|policy||SKIPPED_BY_REQUEST|||||
|KM|policy|inv|OK|63.5600|54.3852|2.5210|1.0000|
|KM|policy|retain|OK|63.5200|54.3849|2.5210|1.0006|
|KM|policy|i1-adaptive|OK|63.5200|54.3849|2.6712|1.0006|

完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。
opportunities 是同一个 INV shadow 的机制机会；不是每种策略的实测性能，也不表示 kernel 内被动复制协议。
