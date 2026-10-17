# 本轮实验结果

wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。
平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。

|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|
|---|---|---|---|---:|---:|---:|---:|
|MM|policy|inv|OK|53.4790|52.2570|1.7171|1.0000|
|MM|policy|retain|OK|53.4790|52.2570|1.5668|1.0000|
|MM|policy|i1-adaptive|OK|53.4790|52.2570|1.4163|1.0000|
|C2D|policy|inv|OK|667.7810|601.5320|14.8423|1.0000|
|C2D|policy|retain|OK|667.5600|601.3110|15.4463|1.0003|
|C2D|policy|i1-adaptive|OK|899.2860|833.0370|18.9002|0.7426|
|ST|policy|inv|OK|83.6660|81.7790|1.7168|1.0000|
|ST|policy|retain|OK|83.6660|81.7790|1.7676|1.0000|
|ST|policy|i1-adaptive|OK|181.3910|179.5100|4.6232|0.4612|
|FIR|policy|inv|OK|24.6530|23.4210|1.6166|1.0000|
|FIR|policy|retain|OK|24.6530|23.4210|1.5160|1.0000|
|FIR|policy|i1-adaptive|OK|24.6530|23.4210|1.4662|1.0000|
|IM2COL|policy|inv|OK|23.8580|22.0430|1.0655|1.0000|
|IM2COL|policy|retain|OK|23.8580|22.0430|1.0157|1.0000|
|IM2COL|policy|i1-adaptive|OK|23.8580|22.0430|1.0657|1.0000|
|BFS|policy|inv|OK|1903.3310|1894.7780|42.0976|1.0000|
|BFS|policy|retain|OK|1901.2520|1892.6990|44.2475|1.0011|
|BFS|policy|i1-adaptive|OK|1563.5030|1554.9520|45.4498|1.2174|
|SC|policy|inv|OK|26.7470|25.3120|3.8702|1.0000|
|SC|policy|retain|OK|26.7470|25.3120|3.7214|1.0000|
|SC|policy|i1-adaptive|OK|26.7470|25.3120|3.6702|1.0000|
|MT|policy|inv|OK|654.0170|651.8150|14.3904|1.0000|
|MT|policy|retain|OK|654.0170|651.8150|14.0887|1.0000|
|MT|policy|i1-adaptive|OK|654.0170|651.8150|14.6902|1.0000|
|FFT|policy|inv|OK|49.3410|47.9050|1.7670|1.0000|
|FFT|policy|retain|OK|49.3410|47.9050|1.6666|1.0000|
|FFT|policy|i1-adaptive|OK|49.3410|47.9050|1.5666|1.0000|
|PR|policy|inv|OK|64.3900|62.6670|2.1673|1.0000|
|PR|policy|retain|OK|64.3900|62.6670|2.1677|1.0000|
|PR|policy|i1-adaptive|OK|71.7810|70.0580|2.3187|0.8970|
|BS|policy|inv|OK|1035.0370|1002.7130|10.1321|1.0000|
|BS|policy|retain|OK|1035.0370|1002.7130|10.5332|1.0000|
|BS|policy|i1-adaptive|OK|3045.3160|3013.2740|59.3773|0.3399|
|LeNet|policy||SKIPPED_BY_REQUEST|||||
|VGG16|policy||SKIPPED_BY_REQUEST|||||
|KM|policy|inv|OK|118.0200|112.1360|3.0690|1.0000|
|KM|policy|retain|OK|117.9810|112.0970|3.1190|1.0003|
|KM|policy|i1-adaptive|OK|117.9810|112.0970|3.3198|1.0003|

完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。
kernel-internal-online-v1 按运行中的实际访存触发，kernel_us 已包含 kernel 内的缓存控制和复制等待。
仅历史 kernel-barrier 结果的 opportunities 属于阶段 shadow；不能与在线协议结果混合计算收益。

|应用|策略|kernel 内 refill|kernel 内 repair|修复后再次写失效|写期间干净副本读|adaptive 按需/全补|
|---|---|---:|---:|---:|---:|---:|
|MM|inv|0|0|0|0|0/0|
|MM|retain|0|0|0|0|0/0|
|MM|i1-adaptive|0|0|0|0|0/0|
|C2D|inv|23|0|14|0|0/0|
|C2D|retain|0|23|14|0|0/0|
|C2D|i1-adaptive|0|106|3|30|106/0|
|ST|inv|0|0|0|0|0/0|
|ST|retain|0|0|0|0|0/0|
|ST|i1-adaptive|0|0|0|0|0/0|
|FIR|inv|0|0|0|0|0/0|
|FIR|retain|0|0|0|0|0/0|
|FIR|i1-adaptive|0|0|0|0|0/0|
|IM2COL|inv|0|0|0|0|0/0|
|IM2COL|retain|0|0|0|0|0/0|
|IM2COL|i1-adaptive|0|0|0|0|0/0|
|BFS|inv|749|0|781|0|0/0|
|BFS|retain|0|750|781|0|0/0|
|BFS|i1-adaptive|0|237|250|574|82/187|
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
|BS|inv|0|0|162|0|0/0|
|BS|retain|0|0|162|0|0/0|
|BS|i1-adaptive|0|0|162|0|0/162|
|KM|inv|0|0|0|0|0/0|
|KM|retain|0|0|0|0|0/0|
|KM|i1-adaptive|0|0|0|0|0/3|
