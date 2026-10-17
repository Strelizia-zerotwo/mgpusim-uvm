# 当前接入状态：核心三策略

2026-10-16，目标源码 `/home/only/projects/mgpusim-uvm/mgpusim`。

| 集合 | 接入 | 默认行为 | 验证边界 |
|---|---|---|---|
| MM/C2D/ST/FIR/IM2COL/BFS/SC/MT/FFT/PR/BS/KM | core-kernel-barrier-v1 | 运行 INV、retain、i1-adaptive | 12 个应用的 smoke 三策略均已实跑通过；最终版复核结果见 VALIDATION.md |
| LeNet | 已接入共享模型的 kernel 分区、动态张量生命周期、独立 CPU 训练校验 | 按用户要求跳过 | 一批 smoke 已完成 record 和三策略；不等于真实 MNIST 的完整训练验证 |
| VGG16 | 已接入同一路径，提供显式 32×32 自检与真实 224×224 数据集入口 | 按用户要求跳过 | 已编译；耗时 GPU 检查按用户要求停止，不记为已通过 |
| FW 补充项 | 保留 parallel-fw 专用 adapter | 显式选择 FW | 原专用布局与本轮通用调度需分开标注 |

通用入口支持 kernel 间主机写入、临时分配与释放。每种策略必须验证相同物理 home、共同计划和数值结果；普通应用还比较逐阶段写出数据摘要。
BFS 使用明确保存的保守读写上界与最终 CPU/终态摘要，处理竞争发现导致的写者变化。不能把保守声明掩码称为实测 dirty。

默认主线为 12 个应用。保留 14 项文献核心名录，不把跳过的两项统计为完成。
当前仍是全局 kernel-barrier oracle；adaptive 前视未来读集；PTE/TLB 元数据免费，实际缓存屏障计费。没有 kernel 内被动权限协议，没有独立 S4 端到端对照。

代码变化镜像在 Windows 工作区 `implementation/core-adapter/mgpusim/`；Linux 覆盖前的逐文件备份在 `results/benchmark/_adapter-backups/`。
