# 本轮实验结果

wall_s 是宿主运行耗时；application_us 是复制模型应用时间；kernel_us 是模拟 kernel 时间。
平台检查的 application_us / speedup / refill 留空。FAIL / BLOCKED / TIMEOUT 不进入收益统计。

|应用|模式|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|
|---|---|---|---|---:|---:|---:|---:|
|MM|policy|inv|OK|40.9990|39.7670|1.3661|1.0000|
|MM|policy|retain|OK|40.9990|39.7670|1.3163|1.0000|
|MM|policy|i1-adaptive|OK|40.9990|39.7670|1.3162|1.0000|
|C2D|policy|inv|OK|725.3950|658.9910|21.0845|1.0000|
|C2D|policy|retain|OK|698.1420|631.7380|19.4601|1.0390|
|C2D|policy|i1-adaptive|OK|698.1420|631.7380|20.5585|1.0390|
|ST|policy|inv|OK|69.4300|67.5390|1.3665|1.0000|
|ST|policy|retain|OK|69.4300|67.5390|1.5640|1.0000|
|ST|policy|i1-adaptive|OK|69.4300|67.5390|1.6622|1.0000|
|FIR|policy|inv|OK|21.6000|20.3680|2.0713|1.0000|
|FIR|policy|retain|OK|21.6000|20.3680|2.0697|1.0000|
|FIR|policy|i1-adaptive|OK|21.6000|20.3680|2.0771|1.0000|
|IM2COL|policy|inv|OK|60.8760|59.0580|2.0832|1.0000|
|IM2COL|policy|retain|OK|51.8790|50.0610|1.7143|1.1734|
|IM2COL|policy|i1-adaptive|OK|51.8790|50.0610|1.6972|1.1734|
|BFS|policy|inv|OK|675.0770|666.4720|31.1805|1.0000|
|BFS|policy|retain|OK|656.8880|648.2830|29.2731|1.0277|
|BFS|policy|i1-adaptive|OK|345.3670|336.7620|25.5199|1.9547|
|SC|policy|inv|OK|74.6720|73.2370|4.0217|1.0000|
|SC|policy|retain|OK|60.4670|59.0320|3.8205|1.2349|
|SC|policy|i1-adaptive|OK|60.4670|59.0320|3.7708|1.2349|
|MT|policy|inv|OK|81.1390|78.8870|5.8252|1.0000|
|MT|policy|retain|OK|81.1390|78.8870|6.0259|1.0000|
|MT|policy|i1-adaptive|OK|81.1390|78.8870|5.9285|1.0000|
|FFT|policy|inv|OK|48.2520|46.8160|1.4171|1.0000|
|FFT|policy|retain|OK|48.2520|46.8160|1.4166|1.0000|
|FFT|policy|i1-adaptive|OK|48.2520|46.8160|1.3679|1.0000|
|PR|policy|inv|OK|366.1500|364.4210|9.6355|1.0000|
|PR|policy|retain|OK|282.6210|280.8920|7.8795|1.2956|
|PR|policy|i1-adaptive|OK|282.6210|280.8920|7.8301|1.2956|
|BS|policy|inv|OK|1156.7320|1123.8080|10.0372|1.0000|
|BS|policy|retain|OK|1118.5950|1085.6710|9.3326|1.0341|
|BS|policy|i1-adaptive|OK|1118.5910|1085.6670|9.0818|1.0341|
|LeNet|policy||SKIPPED_BY_REQUEST|||||
|VGG16|policy||SKIPPED_BY_REQUEST|||||
|KM|policy|inv|OK|102.9450|97.0210|3.3703|1.0000|
|KM|policy|retain|OK|98.2900|92.3660|2.9196|1.0474|
|KM|policy|i1-adaptive|OK|98.2900|92.3660|2.9191|1.0474|

完整命令、日志、原始 SQLite、展开的 metrics.tsv、策略 stats.json、plan 和资源统计见各应用目录。
kernel-internal-lazy-v2 按运行中的实际访存触发，kernel_us 已包含 kernel 内的缓存控制和复制等待。
仅历史 kernel-barrier 结果的 opportunities 属于阶段 shadow；不能与在线协议结果混合计算收益。

|应用|策略|kernel 内 refill|kernel 内 repair|修复后再次写元数据|写期间干净副本读|adaptive 按需/全补|
|---|---|---:|---:|---:|---:|---:|
|MM|inv|0|0|0|0|0/0|
|MM|retain|0|0|0|0|0/0|
|MM|i1-adaptive|0|0|0|0|0/0|
|C2D|inv|91|0|0|0|0/0|
|C2D|retain|0|86|0|0|0/0|
|C2D|i1-adaptive|0|86|0|0|86/0|
|ST|inv|0|0|0|0|0/0|
|ST|retain|0|0|0|0|0/0|
|ST|i1-adaptive|0|0|0|0|0/0|
|FIR|inv|0|0|0|0|0/0|
|FIR|retain|0|0|0|0|0/0|
|FIR|i1-adaptive|0|0|0|0|0/0|
|IM2COL|inv|31|0|0|0|0/0|
|IM2COL|retain|0|31|0|0|0/0|
|IM2COL|i1-adaptive|0|31|0|0|31/0|
|BFS|inv|796|0|584|0|0/0|
|BFS|retain|0|778|576|0|0/0|
|BFS|i1-adaptive|0|180|174|952|97/114|
|SC|inv|43|0|0|0|0/0|
|SC|retain|0|43|0|0|0/0|
|SC|i1-adaptive|0|43|0|0|43/0|
|MT|inv|0|0|0|0|0/0|
|MT|retain|0|0|0|0|0/0|
|MT|i1-adaptive|0|0|0|0|0/0|
|FFT|inv|0|0|0|0|0/0|
|FFT|retain|0|0|0|0|0/0|
|FFT|i1-adaptive|0|0|0|0|0/0|
|PR|inv|426|0|1|0|0/0|
|PR|retain|0|423|1|0|0/0|
|PR|i1-adaptive|0|423|1|0|423/0|
|BS|inv|165|0|67|0|0/0|
|BS|retain|0|165|67|0|0/0|
|BS|i1-adaptive|0|165|67|0|165/162|
|KM|inv|53|0|0|0|0/0|
|KM|retain|0|54|0|0|0/0|
|KM|i1-adaptive|0|54|0|0|54/3|

缓存控制、home fetch、复制服务为并发任务耗时之和，已包含在 kernel_us 中，不可重复相加。
|应用|策略|写转换|过期子块放行|副本维护次数|副本维护字节|并发任务峰值|缓存控制 µs|
|---|---|---:|---:|---:|---:|---:|---:|
|MM|inv|0|0|16|65536|3|0.048|
|MM|retain|0|0|16|65536|3|0.048|
|MM|i1-adaptive|0|0|16|65536|3|0.048|
|C2D|inv|0|0|167|684032|4|5.382|
|C2D|retain|0|0|76|311296|4|5.195|
|C2D|i1-adaptive|0|0|76|311296|4|5.195|
|ST|inv|0|0|8|32768|3|0.024|
|ST|retain|0|0|8|32768|3|0.024|
|ST|i1-adaptive|0|0|8|32768|3|0.024|
|FIR|inv|0|0|6|24576|2|0.018|
|FIR|retain|0|0|6|24576|2|0.018|
|FIR|i1-adaptive|0|0|6|24576|2|0.018|
|IM2COL|inv|0|0|34|139264|1|0.102|
|IM2COL|retain|0|0|3|12288|1|0.04|
|IM2COL|i1-adaptive|0|0|3|12288|1|0.04|
|BFS|inv|0|0|865|3543040|4|6.162|
|BFS|retain|0|0|806|373376|4|5.963|
|BFS|i1-adaptive|0|0|237|325056|4|4.332|
|SC|inv|0|0|49|200704|4|0.147|
|SC|retain|0|0|6|24576|4|0.061|
|SC|i1-adaptive|0|0|6|24576|4|0.061|
|MT|inv|0|0|192|786432|4|0.576|
|MT|retain|0|0|192|786432|4|0.576|
|MT|i1-adaptive|0|0|192|786432|4|0.576|
|FFT|inv|0|0|0|0|0|0.0|
|FFT|retain|0|0|0|0|0|0.0|
|FFT|i1-adaptive|0|0|0|0|0|0.0|
|PR|inv|0|0|441|1806336|3|1.323|
|PR|retain|0|0|15|61440|3|0.468|
|PR|i1-adaptive|0|0|15|61440|3|0.468|
|BS|inv|0|0|330|1351680|1|0.99|
|BS|retain|0|0|165|664576|1|0.66|
|BS|i1-adaptive|0|0|165|664576|1|0.66|
|KM|inv|0|0|83|339968|4|0.249|
|KM|retain|0|0|30|110976|4|0.144|
|KM|i1-adaptive|0|0|30|110976|4|0.144|
