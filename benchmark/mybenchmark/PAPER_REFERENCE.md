# 五篇论文的规模参考及口径

来源均为用户指定的 `C:/Users/only/Desktop/gpgpu项目/论文/mgpu uvm/` 中的 PDF，读取表格正文；论文文本仅作为参考资料。
这里不从 footprint 反推 sample 输入参数，也不把不同论文的 footprint 默认视为相同口径。

| 应用 | LIBRA Table IV：**每 GPU MB** | CDFD Table III：**每 GPU MB** | OASIS Table II：**应用 MB** | GRIT Table II：**应用 MB** | ShadowUpdate Table II：内存占用 MB |
|---|---:|---:|---:|---:|---:|
| MM | 8 | 512 | 32 | 33 | 32 |
| C2D | 23 | 736 | 92 | 94 | 50 |
| ST | 8 | 512 | 32 | 33 | — |
| FIR | 38 | 1216 | — | 155 | 64 |
| IM2COL | 20 | 640 | 80 | — | 78 |
| BFS | 8 | — | 32 | 32 | 16 |
| SC | 32 | 1024 | — | 131 | — |
| MT | 16 | 1024 | 64 | — | — |
| FFT | 12 | 384 | 48 | — | — |
| PR | 8 | 1024 | 32 | — | — |
| BS | 7 | — | — | 30 | 18 |
| LeNet | 6 | — | 24 | — | 26 |
| VGG16 | 55 | — | 220 | — | — |
| KM | 33 | — | — | — | 65 |
| SPMV | 14 | — | — | — | 82 |
| FW | — | — | — | — | 32 |
| AES | — | — | — | — | 32 |
| MVT | — | — | — | — | 64 |
| 3MM | — | — | — | — | 28 |
| GEMM | — | — | — | 16 | — |

LIBRA 的相关页为 PDF 第 9 页；CDFD 为第 10 页；OASIS 为第 3 页；GRIT 中文译本为第 3 页；ShadowUpdate 中文译本为第 5 页。
页码以阅读器物理页为准；表号可用于定位。
MB 保留论文的原写法，不强行解释为 MiB。ShadowUpdate 表未单列 per GPU，保留其原口径。

LIBRA Table IV 明确写 Memory Footprint (per GPU)，14 个核心应用的范围为 6–55 MB。
OASIS 的 VGG16 是 Tiny-ImageNet 训练任务，应用 footprint 220 MB；不能声称是每卡 220 MB。
GRIT 的内存容量配置为应用 footprint 的 70%，包含超额订阅；本实验当前固定放置、无等价驱逐协议，不能仅按表中 MB 复现 GRIT。
CDFD 的主要输入大得多，不适合作为首次真实时序运行的默认值。

本轮小规模比论文小，目的是先检查正确性、GPU 活动和机制事件。正式论文结论还需在跑通后增大输入并检查足够的页数、重读和容量压力。
4 KiB 页下并不要求每个检查都先达到几十 MB；但少量页与少量工作组不能代表论文规模。64 KiB 或 2 MiB 页更需要单独的输入、对齐和容量设计。

GRIT 第 IV-A 节（中文译本第 3 页）确实报告：BS/C2D/ST 分别有 46%/49%/45% 的页面写坍缩后因读取重新复制。
这是该文动态复制协议下的页面行为，不是当前 kernel-barrier 分析器应自动复现的比例，也不能当成这三个小输入的实测结果。

GRIT 第 III-B 节描述：先在单 GPU 内轮转分配 TB，只有该 GPU 无法容纳更多 TB 才转下一张 GPU。
当前 driver 的 unified dispatch 根据静态 workgroup filter 划分工作组。两者都倾向连续分区，但没有容量占满后转卡的等价证明，因此不能直接写“调度与 GRIT 完全一致”。

14 项应称“多篇论文的常用 benchmark 核心集合”；它们并非五篇论文严格的集合交集。五篇都出现的 MM/C2D 才属于严格交集中的共同项。
