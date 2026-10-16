# INV 配置单：所有者放置 v3

源码 `/home/only/projects/mgpusim-uvm/mgpusim`，实验入口在其父目录。INV定义为**静态所有者 home + 只读页面复制 + 写阶段撤销副本 + oracle 屏障预取**。

```bash
cd /home/only/projects/mgpusim-uvm
export PATH=/usr/local/go/bin:/home/only/go/bin:$PATH
python3 benchmark/replica-recovery/run.py \
  --workload parallel-stencil   \
  --size 258 \
  --columns 258 \
  --iterations 4   \
  --home-placement owner \
  --page-bytes 4096   \
  --policies norep,inv,i1,i1-full-stale,i1-adaptive   \
  --link-ns 200 \
  --fault-ns 900
```

| 项目 | 当前共同配置 |
| --- | --- |
| GPU | 4个独立AMD r9nano模型，64 CU/GPU，原有计算和缓存模型 |
| 页面 | 默认4 KiB，32 B子块；硬件MMU/TLB/分配器页大小同步 |
| 物理home | 新分配时直接放到负责该行分区的GPU，Unified=true；不经过执行中的remap |
| 页对齐 | 默认给网格加前置padding，使首个内部行和各分区边界都按页对齐 |
| 所有者验证 | 每个受管理页报告真实home/readers/writers；页对齐owner运行要求非home写事务为0 |
| NoRep/INV/各I-1映射 | 同一plan免费预装映射，同样清TLB；回放mapping_faults=0 |
| 复制固定服务 | 默认900ns；可扫20000ns；16个并发服务槽 |
| 带宽 | 300 GB/s十进制预算，副本和普通RDMA共享源/目的带宽 |
| 附加传播 | 单向200ns；可扫0/100/200/500ns，叠加原有RDMA阶段，未做硬件标定 |
| fabric | 每方向128条有限在途消息，原有1条/周期宽度；按目的地流的就绪头发送，保留同目的地FIFO和反压 |
| 缓存边界 | 所有策略每阶段drain、flush/invalidate L1/L2，清TLB；实际缓存操作时间计入分母 |
| 副本容量 | 真实物理帧；默认无额外页数预算，仍受物理空间约束；可设replica-pages-per-gpu |
| 时间分母 | application_ps = kernel_ps + barrier_flush_ps + recovery_wait_ps |

INV首次只读时整页复制；写阶段撤销该页副本；再次只读时整页重新复制。冲突页按页/阶段回退home，合并全部写者脏集并只提交一次版本。

`--home-placement gpu1`用于保持相同布局和调度的旧放置对照。`--unaligned-owner-pages`取消前置对齐：跨分区的页放到拥有最多字节的GPU，仍可能发生边界页远端写，必须查看non_owner_write_transactions。

默认258×258、每GPU64个内部行、pitch=272个float，每GPU内部区69632 B=17页，支持4 KiB精确对齐。64 KiB页下这个形状不满足分区页对齐，脚本会明确拒绝；可选择足够大的对齐分区，或显式使用非对齐模式并报告共享写页。不能宣称同一物理页同时属于两张GPU。

仍为静态初始放置，没有动态首次触碰协议或写时迁移。复制数据真实写入物理副本，按实际字节付fabric预算；尚未逐请求进入DRAM控制器争用。完整动态写保护缺页和按页缓存失效未实现。
