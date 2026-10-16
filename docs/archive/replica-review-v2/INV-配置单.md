# INV 配置单（审查修复 v2）

对照定义：**固定 home + 读复制 + oracle 屏障预取**。没有写时迁移，不代表真实 UVM 的完整 INV 驱动。

```bash
cd /home/only/projects/mgpusim-uvm
python3 benchmark/replica-recovery/run.py --workload parallel-stencil \
  --size 130 --columns 258 --iterations 4 \
  --policies norep,inv,i1,i1-write-prefetch \
  --link-ns 0,100,200,500 --fault-ns 900,20000
```

| 项目 | 所有对照共同采用的设置 |
| --- | --- |
| 平台 | 4 个独立 AMD r9nano 模型，64 CU/GPU，原有缓存与计算模型 |
| 页大小 | 默认 4096 B；可指定 65536 B，分配器/MMU/TLB 同步改变 |
| home | 统一内存分配原位置通常为 GPU1；读写都可远端访问；不迁移 |
| 映射准备 | **NoRep / INV / I-1 均按同一 plan 免费安装每个活跃 GPU/page 的映射** |
| TLB | 所有回放策略每阶段同样清空 TLB，零 shootdown 时间 |
| 映射缺页 | 本次 oracle 回放中 managed mappings 的需求缺页为零，脚本强制检查 |
| 复制缺页 | 每个复制/修复 GPU/page 事件付固定服务；16 槽，默认扫 900 / 20000 ns |
| 带宽预算 | 300 GB/s，十进制；副本传输与普通 RDMA 共用源注入/目的接收预算 |
| 链路附加延迟 | 扫 0/100/200/500 ns；是原 RDMA 阶段之外的单向附加传播假设，不是 NVLink 实测值 |
| 链路在途队列 | 每 RDMA 出口方向 128 条，发送宽度继承模型的 1 条/周期；可重叠传播，满时反压 |
| 公共缓存屏障 | 所有模式每阶段实际 drain / flush / invalidate L1/L2；不保留跨阶段 L2 复用 |
| 容量 | 使用真实副本帧；默认无额外预算，受原有 4 GiB/GPU 空间约束；可设副本页数预算 |
| 输出分母 | application_ps = kernel_ps + barrier_flush_ps + recovery_wait_ps；强制守恒 |

INV 策略本身：

1. 首次只读阶段复制整页到本地帧。
2. 写阶段开始前撤销整页副本，后续该阶段访问 home。
3. 再次进入只读阶段时整页重新复制；有效副本不会重复复制。
4. 一页多写者，或其他 GPU 的读掩码与写集重叠：**只让该页该阶段所有 GPU 使用 home**。其他页照常复制，运行继续。
5. 结束阶段时合并该页所有写者的 dirty 集，更新一次页版本。

NoRep 现已与其他策略公平化：相同的免费映射准备、TLB 清理和缓存屏障，只是不复制数据。第一遍 record 仍走正常 demand 缺页，仅用来记录 plan，不能将它的时间当作 NoRep 对照。

数据在 backing storage 上功能性复制，并按实际字节计传输预算；尚未将每条副本 DMA 注入 DRAM 控制器。这是双方共同的建模限定。
