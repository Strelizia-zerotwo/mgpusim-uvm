# Kernel 内在线复制协议 v2

当前默认标记为 `core-kernel-internal-lazy-v2`，协议为 `kernel-internal-lazy-v2`。支持 `inv`、`retain`、`i1-adaptive`。决策由 kernel 执行中的真实访存触发；没有未来 trace、未来写集或编译器分析。

## 写入与读侧验证

写请求只更新 home 页版本、被写子块的最后写版本和在途写计数，实际数据仍走正常缓存/RDMA 路径写入 home。写路径不遍历远端副本、不释放帧、不发缓存控制消息，`write_transitions` 恒为 0。已经启动的同页读侧修复可以让后来的写请求等待该页锁；这属于读侧快照保护，不是写触发的转换。

每次实际读取都在 AT 入口检查有效性，包括 TLB 命中。没有采用“一个 kernel 只在首次 TLB miss 检查”的假设。页内不同子块可以有不同的写者；不把同页不同地址的正常访问笼统当成数据竞争。

|策略|读取行为|副本帧处理|
|---|---|---|
|INV|整页版本一致才可读；版本变旧后整页重复制|写时逻辑失效；物理回收/复用推迟到安全的读侧容量处理|
|retain|页版本变旧后，补齐全部过期子块再恢复整页权限|保留旧帧及未改动的数据|
|i1-adaptive|所需子块版本仍有效则本地读；否则在线选择按需修复或全过期修复|保留帧，并保持逐子块版本|

INV 延迟回收的旧帧不能提供差量修复，重新读取仍支付整页搬运。延迟回收用于保护在途读取和处理旧物理缓存标签，不把 INV 变成 retain。

GPU 一旦在本 kernel 写过某页，该 GPU 对该页的所有后续读取都走 home。其他 GPU 仍可读取该页副本中有效的子块，保留 S3 的意义。home 读取也会按需失效请求 GPU 中对应的旧 L1 行；home 的 L2 保持权威，不能被此操作丢弃。

部分修复只更新实际修复子块的 `SectorVersion`。只有整页都已追上 home 时才推进整页版本，防止修复子块 0 后把仍过期的子块 1 错判为有效。

## 修复并发与缓存

每 GPU 一个 FIFO 读侧队列，同一 GPU 一次执行一个维护任务；不同 GPU、不同页的任务可以并行。同一页的任务串行，并等待该页已经准入的请求完成。排队请求在实际开始维护前会重新检查；若前一个读者已补齐所需数据，直接放行，不重复收取复制服务。原始请求保留在有限 AT 输入 FIFO 中，因此同一 AT 仍可能发生队首阻塞。这里没有宣称所有无关访存都完全不受带宽或队列竞争影响。

副本修复采用以下顺序：

1. 锁住目标页的准入，等待该页在途请求结束，包括 home 写入。
2. 向目标 GPU 发送 `CmdInvalidateCleanRange`，只处理被修复/复用物理帧对应的缓存行。缓存继续运行；命令等待匹配的事务和锁定行完成，保留无关热缓存行。复用帧在发布前清理全部旧行。
3. 通过真实的 home L2 端口发出读取，取得包含尚未写回显存的最新数据。源端 cache miss、排队和返回通过模拟器缓存管线执行。
4. 支付固定服务、参数化链路延迟与带宽竞争，将返回的字节写到副本帧的 backing storage，再放行原请求。

源数据走真实 L2 读请求，因此 kernel 内 `home_flush_events=0` 是此实现的预期结果。这里没有通过旧的 GlobalStorage 内容直接取权威数据。目标显存写入仍采用功能性直接写入，并未另外建模目标 DRAM 写事务/队列。

AT 翻译后的物理请求统一使用 `PID=0`；协议直接读取 L2 和按物理地址失效也使用这个命名空间。不能把虚拟进程 PID 填进物理缓存请求，造成同地址两份缓存行。

副本容量采用读侧 LRU；INV 先回收已经逻辑失效的空闲帧，再驱逐有效副本。受害帧必须没有在途 lease，也不能已经被承诺给尚未准入的读取；清理后才能复用。预算或物理分配无法满足时回退 home，不因复制分配失败而 panic。`ReplicaPagesPerGPU=0` 表示不设置实验预算，物理显存仍有限。

## 在线 adaptive 和同步边界

Adaptive 只记住上一段实际读到的子块集合。根据历史预测后续可能再读哪些旧子块，比较一次补齐全部过期子块与多次按需服务的估计成本。无历史时按需修复；预测不会赋予有效性。选择全补包含动态的额外搬运，可以视为对旧副本其余过期子块的历史驱动预取；没有编译器预取，也没有另一个可执行的 `i1-write-prefetch` 在线核心策略。

估计成本使用固定服务、链路延迟和搬运字节；它并不预知共享链路的未来竞争，也没有准确预测未来 L2 命中。策略可能变慢，需要保留反例。

Kernel 边界继续处理实际缓存写回、失效、host I/O 和结果检查，不提供下一 kernel 的访问计划。每个边界还检查版本仍有效的副本子块是否与真实 home 字节一致；这是功能验证，不是模拟的通信操作。

对含 system-scope 同步的 workload，可在 `catalog.json` 的应用项中标注 `"has_system_scope_sync": true`，或直接运行样例时加 `-core-has-system-scope-sync`。保守处理是：本 kernel 已写过的页，后续读取走 home；未写页仍可复制。这个开关不自动检测 fence，不实现新的 system-scope 原子指令或 release/acquire 指令语义。当前常规路径仍对每个请求验证，未使用只靠 TLB 清空的宽松一致性协议。

## 运行命令

```bash
cd /home/only/projects/mgpusim-uvm
export PATH=/usr/local/go/bin:/home/only/go/bin:$PATH

# 三个单 kernel 用例、三种策略，四轮真实读改写，整个页 CPU 检查。
python3 runs/validate_kernel_internal.py

# 先跑重点小样例。
python3 runs/run_benchmarks.py run \
  --mode policy --bench C2D,BFS,ST,BS,PR --profile smoke \
  --policies inv,retain,i1-adaptive --timeout 600

# 12 个核心应用；LeNet/VGG16 默认按用户要求跳过。
python3 runs/run_benchmarks.py run \
  --mode policy --bench core --profile smoke --timeout 600

# 然后扩大输入；输入大小表仍在 SIZES.md。
python3 runs/run_benchmarks.py run \
  --mode policy --bench BFS,PR,KM --profile small --timeout 1800
```

`--protocol kernel-internal` 是默认值。现有调用方式不变，脚本内部已更新 v2 标记、检查与指标。`--mode record` 只执行真实在线 INV，导出实际访问摘要，不作为未来计划喂给其他策略。

显式 `--protocol kernel-barrier` 才运行历史 kernel 边界模型。FW 旧专用 adapter 属于历史入口。v1、v2、kernel-barrier 的结果不能混用 INV 基准来算收益。

## 数据与时间

每个应用目录为 `results/benchmark/<应用>/<suite>/policy/`。总表位于 `results/benchmark/_suites/<suite>/`，保留命令、原始日志、SQLite、metrics.tsv、stats.json、summary.json/tsv/csv 和 REPORT.md。

优先看这些量：

- `write_transitions`、`stale_read_sectors`：必须为 0。后者是准入版本断言计数；实际数据还由数值校验和边界字节断言验证。
- `cold_copies/refills/repairs/copy_bytes`：INV 的恢复叫 refill；retain/adaptive 的恢复叫 repair。
- `intrakernel_refills/intrakernel_repairs`：同一副本在同 kernel 已复制后又恢复。
- `repairs_by_trigger`：cold、stale-version，以及可能同时出现的 frame-reuse 标记；frame-reuse 是附加维度，不能把三项直接相加。
- `repair_bytes/repair_events/repair_granularities`：不含冷复制的恢复量；32/128/512/4096 B 的字节、块数、连续区间数。
- `replica_invalidate_events/bytes`：逻辑目标范围维护次数和按缓存行对齐的字节覆盖，不是逐个 L1/L2 缓存响应数。
- `home_fetch_requests/bytes/ps`、`home_l1_invalidate_events`、`cache_control_ps_in_kernel`、`concurrent_repairs_high_water`、`fallback_home_reads`。
- `clean_replica_reads_during_write`：当前 kernel 已发生写入、且整页版本仍旧时的有效子块本地读。
- `write_after_repair_invalidations`：兼容字段名，v2 实际表示某页修复后又出现写入的元数据事件，不代表发生缓存失效或 transition。

副本占用及 stale 峰值在读侧维护和 kernel 边界采样，不保证捕获同一模拟周期内的瞬时峰值；不要写成精确连续驻留时间曲线。`write_page_epochs` 是兼容字段，在此路径表示非空写请求/host 写引起的版本更新次数，不是 kernel 数。

```text
application_ps = kernel_ps + barrier_flush_ps + host_transfer_ps
```

kernel_us 已包含读侧等待、缓存维护、home fetch 和复制服务。各内部计时是并发任务耗时之和，彼此可以重叠，不能再次加到 application_us。`recovery_us=0` 只表示没有 kernel 外追加恢复时间。

PTE/TLB 映射预装和每请求目录版本检查仍免费；尚无真实元数据缓存、溢出、目录网络延迟或独立 S4 对照。Fabric 是带宽/延迟抽象，以上时间是模拟模型结果，不能直接当作真实 GPU 性能。

`oracle-envelope` 仍只从已完成的整次应用结果中离线选最短时间；它没有参与在线策略决策。
