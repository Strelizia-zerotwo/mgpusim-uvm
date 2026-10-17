# 本轮实验结果

wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。
平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。

|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|
|---|---|---|---|---:|---:|---:|---:|
|MM|policy|inv|OK|302.3060|300.0540|16.9610|1.0000|
|MM|policy|retain|OK|302.3060|300.0540|16.5153|1.0000|
|MM|policy|i1-adaptive|OK|302.3060|300.0540|16.4120|1.0000|
|C2D|policy|inv|OK|4318.0280|4239.5780|251.2860|1.0000|
|C2D|policy|retain|OK|4277.4790|4199.0280|253.4356|1.0095|
|C2D|policy|i1-adaptive|OK|6754.1220|6675.6720|327.5231|0.6393|
|ST|policy|inv|OK|3432.6410|3428.5420|88.9846|1.0000|
|ST|policy|retain|OK|3447.1800|3443.0840|80.5457|0.9958|
|ST|policy|i1-adaptive|OK|9067.4940|9063.4260|239.7022|0.3786|
|FIR|policy|inv|OK|35.9860|33.7340|13.4507|1.0000|
|FIR|policy|retain|OK|35.9860|33.7340|13.5537|1.0000|
|FIR|policy|i1-adaptive|OK|35.9860|33.7340|13.4548|1.0000|
|IM2COL|policy|inv|OK|89.0140|85.4520|10.5967|1.0000|
|IM2COL|policy|retain|OK|89.0140|85.4520|10.7433|1.0000|
|IM2COL|policy|i1-adaptive|OK|89.0140|85.4520|10.6424|1.0000|
|BFS|policy|inv|OK|51101.4000|51083.5200|1088.6719|1.0000|
|BFS|policy|retain|OK|50781.3960|50763.5170|1144.3403|1.0063|
|BFS|policy|i1-adaptive|OK|33653.0690|33635.1840|937.6978|1.5185|
|SC|policy|inv|OK|76.4430|70.9290|51.9114|1.0000|
|SC|policy|retain|OK|76.4430|70.9290|52.2937|1.0000|
|SC|policy|i1-adaptive|OK|76.4430|70.9290|52.2804|1.0000|
|MT|policy|inv|OK|2383.6600|2378.2700|54.5487|1.0000|
|MT|policy|retain|OK|2383.6600|2378.2700|55.0493|1.0000|
|MT|policy|i1-adaptive|OK|2383.6600|2378.2700|54.7023|1.0000|
|FFT|policy|inv|OK|173.3020|166.9200|41.5969|1.0000|
|FFT|policy|retain|OK|173.3020|166.9200|40.9789|1.0000|
|FFT|policy|i1-adaptive|OK|173.3020|166.9200|40.6672|1.0000|
|PR|policy|inv|OK|128.6220|125.6950|17.7183|1.0000|
|PR|policy|retain|OK|128.7790|125.8470|18.2588|0.9988|
|PR|policy|i1-adaptive|OK|339.4520|336.5250|23.5836|0.3789|
|BS|policy|inv|OK|2703.0310|2627.7810|190.6651|1.0000|
|BS|policy|retain|OK|2703.0310|2627.7810|190.0533|1.0000|
|BS|policy|i1-adaptive|OK|4705.8620|4630.6600|240.6879|0.5744|
|LeNet|policy||SKIPPED_BY_REQUEST|||||
|VGG16|policy||SKIPPED_BY_REQUEST|||||
|KM|policy|inv|OK|561.1570|545.5050|143.3454|1.0000|
|KM|policy|retain|OK|561.0490|545.3970|143.7362|1.0002|
|KM|policy|i1-adaptive|OK|561.0490|545.3970|143.9402|1.0002|

完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。
kernel-internal-online-v1 按运行中的实际访存触发，kernel_us 已包含 kernel 内的缓存控制和复制等待。
仅历史 kernel-barrier 结果的 opportunities 属于阶段 shadow；不能与在线协议结果混合计算收益。

|应用|策略|kernel 内 refill|kernel 内 repair|修复后再次写失效|写期间干净副本读|adaptive 按需/全补|
|---|---|---:|---:|---:|---:|---:|
|MM|inv|0|0|0|0|0/0|
|MM|retain|0|0|0|0|0/0|
|MM|i1-adaptive|0|0|0|0|0/0|
|C2D|inv|330|0|309|0|0/0|
|C2D|retain|0|327|306|0|0/0|
|C2D|i1-adaptive|0|932|133|1169|836/96|
|ST|inv|0|0|207|0|0/0|
|ST|retain|0|0|207|0|0/0|
|ST|i1-adaptive|0|0|207|30|0/414|
|FIR|inv|0|0|0|0|0/0|
|FIR|retain|0|0|0|0|0/0|
|FIR|i1-adaptive|0|0|0|0|0/0|
|IM2COL|inv|0|0|0|0|0/0|
|IM2COL|retain|0|0|0|0|0/0|
|IM2COL|i1-adaptive|0|0|0|0|0/0|
|BFS|inv|24616|0|25027|0|0/0|
|BFS|retain|0|24619|25025|0|0/0|
|BFS|i1-adaptive|0|9192|8037|34472|5102/4560|
|SC|inv|0|0|0|0|0/0|
|SC|retain|0|0|0|0|0/0|
|SC|i1-adaptive|0|0|0|0|0/0|
|MT|inv|0|0|0|0|0/0|
|MT|retain|0|0|0|0|0/0|
|MT|i1-adaptive|0|0|0|0|0/0|
|FFT|inv|0|0|0|0|0/0|
|FFT|retain|0|0|0|0|0/0|
|FFT|i1-adaptive|0|0|0|0|0/0|
|PR|inv|0|0|3|0|0/0|
|PR|retain|0|0|3|0|0/0|
|PR|i1-adaptive|0|0|3|0|0/6|
|BS|inv|0|0|24|0|0/0|
|BS|retain|0|0|24|0|0/0|
|BS|i1-adaptive|0|0|24|0|0/24|
|KM|inv|0|0|6|0|0/0|
|KM|retain|0|0|6|0|0/0|
|KM|i1-adaptive|0|0|6|0|0/9|
