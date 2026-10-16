# Oracle 复制实验 v2：操作与审查修复

工作目录 `/home/only/projects/mgpusim-uvm`，Go 模块在 `mgpusim/`。原来的 `-vm-mode=demand` 仍只表示 UVM 映射缺页；新页面复制通过显式 sample/session 启用。

## 本轮修正

1. `Plan.Validate` 只拒绝格式错误。按页/阶段分类 multiple-writers 和 conservative read/write-overlap，受影响页使用 home，其他页继续实验。提交时合并所有写者的 dirty 集并一次更新版本。回退不会把有数据竞争的程序变正确，应用仍需自身同步和数值检查。
2. NoRep 回放与 INV/I-1 同样预装映射并清空 TLB，mapping_faults 必须为零，mapping_installs / tlb_flush_epochs 必须匹配 plan。record 的正常 demand 路径不再混入性能比较。
3. 主 i1 写阶段只允许已有且所需子块有效的副本；i1-write-prefetch 只额外允许冷副本，两者对已有过期副本的处理相同。新增副本/字节/故障/读取分别报告。
4. 添加实际行分区的四 GPU 九点 Jacobi：每轮全部 GPU 分别计算其行，读取 halo，用非均匀输入和非零中心、邻边、对角权重，逐点和 CPU 结果比较。
5. RDMA 传播延迟不再占住整个出口：128 条有限在途队列允许流水重叠，源注入/目的接收带宽和端口宽度仍有限。drain 等待在途响应实际送出；队列满时反压。附加延迟需要扫描，未做实卡标定。
6. 驱动忽略空队列的迟到唤醒，避免凭空推进一周期。显式分区批次在全局屏障处原子提交，全部 GPU 队列提交完再执行。

## 两个并发调度

```bash
cd /home/only/projects/mgpusim-uvm
# 双缓冲：70 个 managed 页，直接读相邻 halo
python3 benchmark/replica-recovery/run.py --workload parallel-stencil \
  --size 130 --columns 258 --iterations 4 \
  --policies norep,inv,i1,i1-write-prefetch \
  --link-ns 0,100,200,500 --fault-ns 900,20000

# 单全局网格 + 私有旧值快照：内部计算与 halo 读取重叠，再更新边界
python3 benchmark/replica-recovery/run.py --workload parallel-stencil-overlap \
  --size 258 --columns 258 --iterations 2 --page-bytes 65536 \
  --policies norep,inv,i1,i1-write-prefetch \
  --link-ns 0,100,200,500 --fault-ns 900,20000
```

第二个调度每轮有 snapshot-interior、bulk-and-halo、boundary 三个全局阶段：先保存旧分区值，在不同 GPU 队列中并发进行内部更新和邻居 halo 读取，最后用快照计算边界。它与 CPU Jacobi 数学结果一致，完整包含这些额外快照/启动/屏障成本。bulk 不改 halo 正在读取的边界行，避免真实读写数据竞争。

`size-2` 必须是 `16*GPU数` 的倍数，columns-2 必须是64的倍数。重叠调度每卡至少48个内部行；可用4 KiB页作为对照，但页尺度改变可能消除同页的 S3 机会，不能预设收益。

每个阶段记录实际 CU 指令生命周期，输出每 GPU 指令数和有 N 个 GPU 同时存在在途指令的时长；脚本要求四张 GPU 每阶段实际工作且具有四卡重叠时长，不只检查排队数量。

## 定向回归

```bash
python3 benchmark/replica-recovery/run.py --probe-case cold-write \
  --policies norep,inv,i1,i1-write-prefetch --fault-ns 900 --link-ns 200
python3 benchmark/replica-recovery/run.py --probe-case fallback \
  --policies norep,inv,i1,i1-write-prefetch --fault-ns 900 --link-ns 200
```

cold-write 没有最初的冷读阶段，能检查主 i1 不会在写阶段偷偷建副本。fallback 同时覆盖同页多 GPU 的不重叠写，以及实际字节不冲突但64B读取事务保守重叠的情况。

probe 调用的 `MemCopyD2D` 已核实：driver/api.go 中加载 `copyKernel`，通过 `EnqueueLaunchKernel` 执行 GPU 内核，不是 DMA。

## 输出和字段

`results/replica-recovery/<负载>/<时间_v2_唯一ID>/` 包含 record、各策略实际命令/JSON、独立观测 plan、数值日志、SQLite、summary.tsv/json、源 SHA256。

所有用于发布的本轮数据必须有 `model_revision=replica-review-v2` 和 `fabric_model=pipelined-bounded-v1`。标有 SUPERSEDED.txt 的开发结果以及早期 v1 数值不可混用。统计包括：

- fallback_page_epochs、multiple_writer_page_epochs、read_write_overlap_page_epochs：回退覆盖范围；fallback_events逐页记录原因。
- mapping_faults、mapping_installs、tlb_flush_epochs：对照公平性。
- managed_pages、实际副本页数、驱逐和过期页数：容量与工作集。
- write_epoch_existing_replica_reads 与 write_epoch_new_*：实际来源；S3 shadow 计数仅为机会数。
- recovery_events：恢复源版本、实际字节、计费字节、32/128/512/4096 B 块数和连续区间。
- application_ps 三部分时间守恒；完整分母包含公共 flush 和预取等待。

## 结果的解释边界

原先轮换 GPU 的 FW/stencil 不是分区并行应用，两个零收益结果 **不作为支持或反驳 idea 1 的证据**。旧 NoRep 与复制策略的映射处理不一致，旧149us对68us的差距也不再用于比较。

标准双缓冲 Jacobi 每轮读 A 写 B，自然没有 S3。它只测本调度下的 S1/复制成本，不能据此否定 S3。重叠调度可以检查已有副本读的实际发生，但依旧只是一个明确调度和小规模配置，不足以得出规范 benchmark 集的普遍收益。

持续保留的假设：home 固定，完美屏障预取，公共全量 L2 flush/invalidate 和 TLB 清理。它们取消跨 kernel L2 复用，增加分母；当前未实现按受影响页的精细缓存失效。附加链路0/100/200/500 ns没有硬件测量依据，是独立灵敏度参数；传播叠加在原有RDMA/缓存处理之上。300GB/s也是传输预算上限，还受1条/周期的模型端口与128条在途窗口限制。

没有写时迁移、内核内写保护故障、真实元数据记录硬件或完整NVLink拓扑。副本数据直接写 backing storage，再计传输预算；未逐请求竞争 DRAM 控制器。本轮修复不改变这些限定。
