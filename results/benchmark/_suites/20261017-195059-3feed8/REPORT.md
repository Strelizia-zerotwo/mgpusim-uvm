# 本轮实验结果

wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。
平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。

|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|
|---|---|---|---|---:|---:|---:|---:|
|MM|policy|inv|OK|40.9990|39.7670|1.4665|1.0000|
|MM|policy|retain|OK|40.9990|39.7670|1.5167|1.0000|
|MM|policy|i1-adaptive|OK|40.9990|39.7670|1.4667|1.0000|
|C2D|policy|inv|OK|639.1100|572.7060|19.1588|1.0000|
|C2D|policy|retain|OK|639.1100|572.7060|17.9067|1.0000|
|C2D|policy|i1-adaptive|OK|639.1100|572.7060|19.3806|1.0000|
|ST|policy|inv|OK|69.4300|67.5390|1.4163|1.0000|
|ST|policy|retain|OK|69.4300|67.5390|1.4167|1.0000|
|ST|policy|i1-adaptive|OK|69.4300|67.5390|1.4160|1.0000|
|FIR|policy|inv|OK|21.6000|20.3680|1.8166|1.0000|
|FIR|policy|retain|OK|21.6000|20.3680|2.0724|1.0000|
|FIR|policy|i1-adaptive|OK|21.6000|20.3680|2.1425|1.0000|
|IM2COL|policy|inv|OK|23.9170|22.0990|1.3686|1.0000|
|IM2COL|policy|retain|OK|23.9170|22.0990|1.3166|1.0000|
|IM2COL|policy|i1-adaptive|OK|23.9170|22.0990|1.3669|1.0000|
|BFS|policy|inv|OK|297.0620|288.4570|25.3185|1.0000|
|BFS|policy|retain|OK|292.7850|284.1800|24.1641|1.0146|
|BFS|policy|i1-adaptive|OK|280.6460|272.0410|23.6409|1.0585|
|SC|policy|inv|OK|21.6810|20.2460|3.5706|1.0000|
|SC|policy|retain|OK|21.6810|20.2460|3.5250|1.0000|
|SC|policy|i1-adaptive|OK|21.6810|20.2460|3.6959|1.0000|
|MT|policy|inv|OK|81.1390|78.8870|5.7750|1.0000|
|MT|policy|retain|OK|81.1390|78.8870|5.7252|1.0000|
|MT|policy|i1-adaptive|OK|81.1390|78.8870|5.7749|1.0000|
|FFT|policy|inv|OK|48.2520|46.8160|1.3662|1.0000|
|FFT|policy|retain|OK|48.2520|46.8160|1.3657|1.0000|
|FFT|policy|i1-adaptive|OK|48.2520|46.8160|1.3661|1.0000|
|PR|policy|inv|OK|58.7230|56.9940|5.3145|1.0000|
|PR|policy|retain|OK|58.7230|56.9940|5.1726|1.0000|
|PR|policy|i1-adaptive|OK|58.7230|56.9940|5.0249|1.0000|
|BS|policy|inv|OK|966.2590|933.3350|7.7291|1.0000|
|BS|policy|retain|OK|966.2590|933.3350|7.7789|1.0000|
|BS|policy|i1-adaptive|OK|966.2590|933.3350|7.8563|1.0000|
|LeNet|policy||SKIPPED_BY_REQUEST|||||
|VGG16|policy||SKIPPED_BY_REQUEST|||||
|KM|policy|inv|OK|77.7260|71.8020|2.5208|1.0000|
|KM|policy|retain|OK|77.6840|71.7600|2.5184|1.0005|
|KM|policy|i1-adaptive|OK|77.6840|71.7600|2.5180|1.0005|

完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。
kernel-internal-lazy-v2 按运行中的实际访存触发，kernel_us 已包含 kernel 内的缓存控制和复制等待。
仅历史 kernel-barrier 结果的 opportunities 属于阶段 shadow；不能与在线协议结果混合计算收益。

|应用|策略|kernel 内 refill|kernel 内 repair|修复后再次写元数据|写期间干净副本读|adaptive 按需/全补|
|---|---|---:|---:|---:|---:|---:|
|MM|inv|0|0|0|0|0/0|
|MM|retain|0|0|0|0|0/0|
|MM|i1-adaptive|0|0|0|0|0/0|
|C2D|inv|0|0|0|0|0/0|
|C2D|retain|0|0|0|0|0/0|
|C2D|i1-adaptive|0|0|0|0|0/0|
|ST|inv|0|0|0|0|0/0|
|ST|retain|0|0|0|0|0/0|
|ST|i1-adaptive|0|0|0|0|0/0|
|FIR|inv|0|0|0|0|0/0|
|FIR|retain|0|0|0|0|0/0|
|FIR|i1-adaptive|0|0|0|0|0/0|
|IM2COL|inv|0|0|0|0|0/0|
|IM2COL|retain|0|0|0|0|0/0|
|IM2COL|i1-adaptive|0|0|0|0|0/0|
|BFS|inv|22|0|36|0|0/0|
|BFS|retain|0|19|36|0|0/0|
|BFS|i1-adaptive|0|7|19|41|11/15|
|SC|inv|0|0|0|0|0/0|
|SC|retain|0|0|0|0|0/0|
|SC|i1-adaptive|0|0|0|0|0/0|
|MT|inv|0|0|0|0|0/0|
|MT|retain|0|0|0|0|0/0|
|MT|i1-adaptive|0|0|0|0|0/0|
|FFT|inv|0|0|0|0|0/0|
|FFT|retain|0|0|0|0|0/0|
|FFT|i1-adaptive|0|0|0|0|0/0|
|PR|inv|0|0|0|0|0/0|
|PR|retain|0|0|0|0|0/0|
|PR|i1-adaptive|0|0|0|0|0/0|
|BS|inv|0|0|54|0|0/0|
|BS|retain|0|0|54|0|0/0|
|BS|i1-adaptive|0|0|54|0|0/162|
|KM|inv|0|0|0|0|0/0|
|KM|retain|0|0|0|0|0/0|
|KM|i1-adaptive|0|0|0|0|0/3|

缓存控制、home fetch、复制服务为并发任务耗时之和，已包含在 kernel_us 中，不可重复相加。
|应用|策略|写转换|过期子块放行|副本维护次数|副本维护字节|并发任务峰值|缓存控制 µs|
|---|---|---:|---:|---:|---:|---:|---:|
|MM|inv|0|0|16|65536|3|0.048|
|MM|retain|0|0|16|65536|3|0.048|
|MM|i1-adaptive|0|0|16|65536|3|0.048|
|C2D|inv|0|0|76|311296|4|5.109|
|C2D|retain|0|0|76|311296|4|5.109|
|C2D|i1-adaptive|0|0|76|311296|4|5.109|
|ST|inv|0|0|8|32768|3|0.024|
|ST|retain|0|0|8|32768|3|0.024|
|ST|i1-adaptive|0|0|8|32768|3|0.024|
|FIR|inv|0|0|6|24576|2|0.018|
|FIR|retain|0|0|6|24576|2|0.018|
|FIR|i1-adaptive|0|0|6|24576|2|0.018|
|IM2COL|inv|0|0|3|12288|1|0.009|
|IM2COL|retain|0|0|3|12288|1|0.009|
|IM2COL|i1-adaptive|0|0|3|12288|1|0.009|
|BFS|inv|0|0|91|372736|4|4.965|
|BFS|retain|0|0|88|199616|4|4.965|
|BFS|i1-adaptive|0|0|61|176768|4|4.908|
|SC|inv|0|0|6|24576|4|0.018|
|SC|retain|0|0|6|24576|4|0.018|
|SC|i1-adaptive|0|0|6|24576|4|0.018|
|MT|inv|0|0|192|786432|4|0.576|
|MT|retain|0|0|192|786432|4|0.576|
|MT|i1-adaptive|0|0|192|786432|4|0.576|
|FFT|inv|0|0|0|0|0|0.0|
|FFT|retain|0|0|0|0|0|0.0|
|FFT|i1-adaptive|0|0|0|0|0|0.0|
|PR|inv|0|0|15|61440|3|0.045|
|PR|retain|0|0|15|61440|3|0.045|
|PR|i1-adaptive|0|0|15|61440|3|0.045|
|BS|inv|0|0|165|675840|1|0.495|
|BS|retain|0|0|165|675840|1|0.495|
|BS|i1-adaptive|0|0|165|675840|1|0.495|
|KM|inv|0|0|30|122880|4|0.09|
|KM|retain|0|0|30|110976|4|0.09|
|KM|i1-adaptive|0|0|30|110976|4|0.09|
