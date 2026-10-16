# 页面复制实验 v3：所有者放置、修复选择和离线筛选

工作目录 `/home/only/projects/mgpusim-uvm`，Go模块位于 `mgpusim/`。普通 `-vm-mode=demand` 仍为原来的映射缺页模型；本实验通过replica sample显式启用。

## 本轮代码变化

1. 分配器与驱动新增 `AllocateUnifiedMemoryOnGPU`，在初始分配时直接选home，保留Unified=true。默认AllocateUnifiedMemory仍保持原有接口行为。
2. Jacobi按页面实际所有者分配，页对齐布局使每GPU的内部写入全为本地。物理home在首次执行前已经确定，不做执行中迁移或借助页表伪造本地数据。
3. i1-full-stale补齐全部过期子块，i1-adaptive按下一次写之前的再读需求比较两种成本；整次应用oracle选择包络包含INV兜底。
4. fabric发送选择已经到期的目的地流头，避免一个尚未就绪的目的地堵住其他已就绪目的地，保留同目的地FIFO、带宽、有限队列和端口反压。
5. 新增parallel-fw：原始GCN3条件写FW内核按输出行分区，使用原始全局ID、每卡本地代码、所有者页面与全局迭代边界；与原CPU算法逐项校验。
6. 新增screen_plan.py，从真实plan和真实home离线筛选写入稀疏度、跨GPU再读、重复制脏比例；不把低脏比例单独当作收益。

## 首选小规模对照

```bash
cd /home/only/projects/mgpusim-uvm
export PATH=/usr/local/go/bin:/home/only/go/bin:$PATH
python3 benchmark/replica-recovery/run.py \
  --workload parallel-stencil   \
  --size 258 \
  --columns 258 \
  --iterations 4 \
  --home-placement owner   \
  --policies norep,inv,i1,i1-full-stale,i1-adaptive \
  --link-ns 200 \
  --fault-ns 900
```

默认4个独立GPU；网格前置padding让首个内部行和分区边界落在页边界。若修改size/columns/page-bytes导致分区字节不是整页倍数，脚本会拒绝。`--unaligned-owner-pages`保留普通连续布局，按最大字节份额决定页home，并报告残留的远端写。

要控制所有者放置这一个变量，保持全部参数不变，仅使用 `--home-placement gpu1`。不能用不同尺寸或不同布局的结果计算放置收益。

重叠调度：

```bash
python3 benchmark/replica-recovery/run.py \
  --workload parallel-stencil-overlap   \
  --size 258 \
  --columns 258 \
  --iterations 2 \
  --home-placement owner   \
  --policies inv,i1,i1-full-stale,i1-adaptive \
  --link-ns 200 \
  --fault-ns 900 \
  --repeats 2
```

两种调度都是真实四GPU行分区九点Jacobi。重叠调度包含私有旧值快照、内部计算/halo获取、边界计算，完整计入这些额外阶段的时间；两者不是两个独立应用。每阶段检查四卡实际指令在途重叠。

## 先筛选，再做策略回放

```bash
python3 benchmark/replica-recovery/run.py \
  --workload parallel-fw   \
  --size 128 \
  --iterations 12 \
  --page-bytes 4096 \
  --record-only

# 将下面路径换成上一条输出的record目录
python3 benchmark/replica-recovery/screen_plan.py   /home/only/projects/mgpusim-uvm/results/replica-recovery/parallel-fw/<本轮目录>/record   \
  --output-dir /home/only/projects/mgpusim-uvm/results/replica-screen
```

record仍运行真实timing内核以记录访问，但不会运行复制策略性能比较。FW128的4GPU分区满足4KiB页对齐。FW使用非负权重，单次pass的pivot行/列数值不会改变，因此并发分区读取pivot与原算法一致；CPU数值校验仍为必需条件。

screen的written fraction为32 B请求掩码粒度，不是逐元素数值改变比例。它独立报告write-page-epoch稀疏度与非home副本refill：没有跨GPU再读，或者再读时几乎整页脏，都不能仅凭“有条件写”进入S1性能结论。当前FW小配置没有refill，未继续跑整套策略。

## 输出与检查

结果在 `results/replica-recovery/<workload>/<时间_v3_ID>/`：命令、配置、记录与观测plan、物理home列表、日志、SQLite、summary、oracle-envelope，以及与source-sha256匹配的source-archive.tar.gz。各运行二进制也保留。

- owner_write_transactions / non_owner_write_transactions：按真实home区分；精确对齐owner运行要求后者为0。
- non_owner_read_transactions / replica_read_transactions / remote_home_read_transactions：逻辑非home读取、实际副本读取、实际远端home读取，均为翻译器入口事务，不能直接称为DRAM访问。
- page_ownership、home_pages_by_gpu：真实home和完整plan的读写GPU。
- copy_bytes_by_role、copy_faults_by_role：区分owner-written-shared-read、non-owner-writer、read-only。
- recovery_wait_ps_by_role：一个恢复阶段全部事件同类时归到该类，混合阶段保留mixed，不按字节比例强行分摊重叠时间。Jacobi所有者布局中全部恢复均来自halo所属的owner-written-shared-read页。
- mapping_faults / mapping_installs / tlb_flush_epochs：全部回放对照共享映射准备。
- write_epoch_existing_replica_reads与write_epoch_new_*：区分已有与冷副本；S3 shadow只是机会数。

核心限定：静态home、屏障预取、全量L1/L2缓存屏障及TLB清理、附加传播未标定、副本未逐请求争用DRAM、没有完整动态UVM权限协议。按受影响页失效缓存行留待后续工作。早期v2配置在docs/archive/replica-review-v2保留，仅解释当时的固定GPU1实验。
