# 验证记录：所有者放置与修复选择 v3

日期2026-10-16。全部为模拟器结果，平台为四个独立AMD r9nano模型。模型标记replica-owner-v3，fabric标记destination-ready-v2。

## 优先修复的验证

**所有者放置已改变实际物理home。** 同为258×258、4轮、4KiB、相同页对齐布局和逐阶段读写掩码，只更改home：

| INV配置 | home分布/GPU1,2,3,4 | 远端写事务 | 复制/B | 复制事件数 | 应用时间/us |
| --- | --- | ---: | ---: | ---: | ---: |
| 全部GPU1 | 140,0,0,0 | 15360 | 925696 | 226 | 123.349 |
| 分区所有者 | 36,34,34,36 | 0 | 98304 | 24 | 85.084 |

复制字节下降约89.38%。这次放置对照的网格、调度和观测掩码完全一致，不能把这个改进算成idea 1相对INV的收益：正式NoRep/INV/I-1必须全部使用owner放置。

NoRep在相同两个配置的时间分别为137.924和81.556us。owner布局的20480次写事务均由真实home GPU发出，非home读只有672次，全部来自相邻halo页。没有“一边把数据留在GPU1，一边只改本地页表”的伪本地映射。

## halo在分母中的份额

owner Jacobi，258×258，4轮，link附加200ns，固定服务900ns：

| 策略 | 应用时间/us | 复制/B | 复制/修复事件 | 恢复等待/us |
| --- | ---: | ---: | ---: | ---: |
| norep | 81.556 | 0 | 0 | 0.000000 |
| inv | 85.084 | 98,304 | 24 | 4.564484 |
| i1 | 85.022 | 61,824 | 24 | 4.503686 |
| i1-full-stale | 85.082 | 96,960 | 24 | 4.563322 |
| i1-adaptive | 85.022 | 61,824 | 24 | 4.503686 |

INV恢复事件全部属于owner-written-shared-read页，在本Jacobi中就是halo页。其恢复等待4.564484us，占85.084us的**5.364680%**；该比值仅表示当前模型中可归因到这组恢复事件的等待，不是所有UVM影响的数学上界。

按需/前视减少到61824B，总时间只减0.062us（约0.073%）。全过期补齐为96960B，几乎等于INV；离线分析中halo重新复制时的全部过期子块为整页的97.265625%。按需减少更多字节主要来自只补本阶段真正要读取的部分，不能等同于“写入本身很稀疏”。

固定服务20000ns时，同一INV的halo恢复等待80.964484us，占161.484us的**50.137775%**；各策略仍是24次服务，S1只减少字节而不消除服务，因此差量策略总时间仍只少0.062us。不能从50%的等待直接推导50%的差量收益。

## 小规模参数对照

本轮在owner布局测0/200ns与900/20000ns两个维度，共5策略×4配置=20回放。没有把v2的0/100/200/500ns固定GPU1扫描冒充v3扫描。

| 附加传播/ns | 固定服务/ns | NoRep/us | INV/us | i1-adaptive/us | INV恢复占比 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 0 | 900 | 80.139 | 84.284 | 84.222 | 4.466% |
| 200 | 900 | 81.556 | 85.084 | 85.022 | 5.365% |
| 0 | 20000 | 80.139 | 160.684 | 160.622 | 49.890% |
| 200 | 20000 | 81.556 | 161.484 | 161.422 | 50.138% |

owner重叠调度258×258、2轮时，INV为156.070us，按需/前视为156.039us，全过期补齐156.069us。各策略两次独立运行的模拟时间、复制字节和事件数完全一致。当前owner/4KiB调度的S3实际读取为0，不能据此判断S3收益。

微小差值不作为普遍正面/反面结论。模拟时间由事件引擎产生，宿主机wall-clock抖动不直接变成模拟时间；输入、调度、仲裁和模型敏感性仍需要分别检查。

## 重复修复回归：仅用于机制诊断

保留旧GPU1、未对齐的重叠布局，精确复现108→113次事件。此组仍有7680次非home写和6次冲突回退，**不是正式所有者基线**。

| 固定服务/ns | 策略 | 总时间/us | 复制/修复事件 | 复制/B |
| --- | --- | ---: | ---: | ---: |
| 900 | inv | 199.680 | 108 | 442,368 |
| 900 | i1 | 200.786 | 113 | 429,216 |
| 900 | i1-full-stale | 199.669 | 108 | 432,992 |
| 900 | i1-adaptive | 199.669 | 108 | 432,992 |
| 20000 | inv | 352.480 | 108 | 442,368 |
| 20000 | i1 | 372.686 | 113 | 429,216 |
| 20000 | i1-full-stale | 352.469 | 108 | 432,992 |
| 20000 | i1-adaptive | 352.469 | 108 | 432,992 |

全过期补齐/前视都恢复为108次事件，消除了原按需策略额外支付固定服务造成的拖慢。真正严格不高于INV的报告值仍采用包含INV的整次应用oracle-envelope，逐次前视策略本身没有全局不劣证明。

## 局部写负载筛选

新parallel-fw为真实四GPU按行分区，128节点、12轮、4KiB，共32个受管理页；CPU逐项数值校验通过，每阶段四卡均实际执行，四卡指令在途重叠最短0.853us。仅进行record和离线screen，没有做复制策略收益比较。

这份具体plan有380个write-page-epochs，32 B写掩码平均覆盖47.097%的页；没有非home写，但INV shadow的跨GPUread-refill为0。低写比例与重新读取是两个条件，本配置不满足后者，所以未继续整套回放。该47.097%不是逐元素数值改变率，也不能与不同规模/轮数的历史约10%直接比较。

已有旧BFS摘要只显示GPU1活动；普通PageRank的refill几乎整页脏。旧原始bin trace已不在这些结果目录，本轮没有把旧摘要重新包装为新测量，也没有声称实现SSSP、pagerank-delta或embedding更新。后续可用screen_plan.py统一筛选实际分区记录；必须同时报告多GPU活动和副本再读。

## 检查与保留假设

- 42个策略回放全部数值通过、时间守恒，角色等待之和等于recovery_wait_ps，角色字节之和等于copy_bytes。
- 全部回放mapping_faults=0，映射安装与TLB清理匹配同一个plan，观测读写掩码一致；owner对齐配置的非home写为0。
- fallback回归覆盖多写者及64B事务保守重叠，四种复制策略均按页回退并通过数值校验。
- go build ./...、相关driver/allocator/replica/rdma/faultvm/localmapping/cp/runner的race测试通过；全仓lint为0 issues。
- 完整四GPU i1-adaptive的race二进制回放通过，无DATA RACE，总时间85.022us。
- 原demand完整验收通过，记录results/uvm-validation-20261016-165557/validation.json，status=PASS。
- 每轮source-archive.tar.gz与保存的全部Go源码哈希一致。最早owner/FW记录后为lint提取了两个辅助函数；其余最终回放对应当前源码，最终race回放也已验证。

仍保留静态home、完美屏障预取、共同的全量L1/L2与TLB清理、未标定的附加传播、抽象fabric带宽及尚未逐请求竞争DRAM的复制。全量flush会消除跨阶段L2复用。按受影响页做精细失效属于后续改变，本轮没有混入，以便单独评估放置和修复问题。

## 结果目录

全部路径相对于/home/only/projects/mgpusim-uvm；机读索引docs/replica-results.json。

- owner-jacobi：`results/replica-recovery/parallel-stencil/2026-10-16_16-50-13_v3_45d399/summary.tsv`。
- gpu1-placement-control：`results/replica-recovery/parallel-stencil/2026-10-16_16-54-10_v3_ace1ee/summary.tsv`。
- owner-overlap-repeat：`results/replica-recovery/parallel-stencil-overlap/2026-10-16_16-52-56_v3_f74c31/summary.tsv`。
- legacy-repair-regression：`results/replica-recovery/parallel-stencil-overlap/2026-10-16_16-55-59_v3_dbdbcf/summary.tsv`。
- fallback：`results/replica-recovery/probe/2026-10-16_16-54-10_v3_8f04dd/summary.tsv`。
- FW record：`results/replica-recovery/parallel-fw/2026-10-16_16-50-13_v3_6a7df9/record`。
- 离线筛选：`results/replica-screen-v3/screen.json`。
