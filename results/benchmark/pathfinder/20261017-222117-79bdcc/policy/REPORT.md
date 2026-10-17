# 本轮实验结果

application_us 是模拟应用总时间，kernel_us 已包含请求在 kernel 内等待复制/修复的时间；wall_s 是宿主运行耗时。
ideal-v3 的失效、版本元数据、缓存维护、在途正确性处理均免费。只给复制/修复收固定 fault 和总字节传输费用。
失败与超时不计收益；按页大小、协议、链路和固定服务参数分别与 INV 比较。

|应用|页字节|fault ns|link ns|策略|状态|应用 µs|kernel µs|宿主秒|INV 加速比|节省 %|
|---|---:|---:|---:|---|---|---:|---:|---:|---:|---:|
|pathfinder|4096|900|200|inv|OK|150.2150|148.7600|22.7717|1.0000|0.0000|
|pathfinder|4096|900|200|delta|OK|150.2150|148.7600|16.2824|1.0000|0.0000|
|pathfinder|4096|900|200|delta-sub|OK|150.2150|148.7600|16.2664|1.0000|0.0000|

|应用|页字节|fault ns|link ns|策略|冷复制|整页 refill|差量 repair|传输字节|失效|保留帧峰值|过期保留帧峰值|
|---|---:|---:|---:|---|---:|---:|---:|---:|---:|---:|---:|
|pathfinder|4096|900|200|inv|51|39|0|368640|42|0|0|
|pathfinder|4096|900|200|delta|51|0|39|368640|42|51|6|
|pathfinder|4096|900|200|delta-sub|51|0|39|368640|42|51|6|

|应用|页字节|fault ns|link ns|策略|kernel 内 refill|kernel 内 repair|干净子块免修复读|复制服务累计 µs|功能读修正|过期子块放行|
|---|---:|---:|---:|---|---:|---:|---:|---:|---:|---:|
|pathfinder|4096|900|200|inv|0|0|0|100.802732|0|0|
|pathfinder|4096|900|200|delta|0|0|0|100.802732|0|0|
|pathfinder|4096|900|200|delta-sub|0|0|0|100.802732|0|0|

复制服务累计时间是并发任务耗时之和，已包含在 kernel_us 中，不能重复相加。
functional_read_corrections 统计理想功能层替换后端读值的次数；它与过期副本被错误放行不同，详见 KERNEL_INTERNAL.md。
INV 逻辑上立即释放失效帧；后端在途读为防物理地址重用而暂留的帧，另记 peak_backend_pinned_frames。
隔离的同一复制事件上 delta 费用不大于 INV，infinity 为零；应用异步调度可能改变事件数与缓存行为，反向次序原样保留并标警告。

原始日志、命令、stats.json、metrics.sqlite3、metrics.tsv、observed-accesses.json 与资源统计见各应用目录。
observed-accesses 仅记录执行事实，不参与后续请求的策略决策。旧版 kernel-barrier/v2 结果具有不同成本口径。
