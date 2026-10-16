# 当前接入状态与实验证据边界

检查对象：`/home/only/projects/mgpusim-uvm/mgpusim`，2026-10-16。
当前源码已恢复独立的 replica 显式实验入口；更早“只保留 demand”的历史记录不代表当前状态。

| 应用 | stock/shared 路径 | 独立 GPU demand 路径 | 7 策略真实时序回放 |
|---|---|---|---|
| MM/FIR/SC/MT/FFT/PR/BS/KM | 已提供脚本；须看数值校验与真实 GPU 活动 | 依赖各 sample 的分区实现，脚本做活动/缺页检查 | 尚无 adapter |
| ST | 原 stencil2d sample | 原 sample 可做平台检查 | 已有 `parallel-stencil` adapter，真实行分区、静态 owner、逐值 CPU 校验 |
| C2D/IM2COL | unified 虚拟 GPU 路径可尝试；启用逐算子校验；张量分配未使用 sample 中的 useUnifiedMemory 字段 | sample 的 SelectGPU 明确拒绝多个物理 GPU | 尚无 adapter |
| BFS | unified 虚拟 GPU 路径可尝试 | sample 的 SelectGPU 明确拒绝多个物理 GPU | 尚无 adapter |
| LeNet/VGG16 | 普通训练代码存在；本套 UVM 清单明确记为阻塞 | SetUnifiedMemory 直接 panic；Verify 未实现 | 尚无 adapter |
| FW（补充项） | 原 sample | 原 sample 分工与并行 adapter 不是同一实现 | 已有 `parallel-fw`，真实行分区、逐值 CPU 校验 |
| 其他 B/C 应用 | 目录存在，已给参数；未声称全部运行成功 | 未逐项建立正式需求 | 尚无 adapter |

证据位置：

- `amd/samples/replica/main.go` 的 workload switch 只接入 probe、parallel-stencil、parallel-stencil-overlap、parallel-fw、轮换式 fw/stencil。
- `benchmark/replica-recovery/run.py` 提供策略配置、共同 plan、映射与并行活动检查。
- `amd/benchmarks/shoc/bfs/bfs.go`、`dnn/layer_benchmarks/{conv2d,im2col}/benchmark.go` 的 SelectGPU 有显式限制。
- `dnn/training_benchmarks/{lenet,vgg16}/benchmark.go` 的 SetUnifiedMemory 和 Verify 是明确阻塞。
- `amd/samples/runner/faultflags.go` 明确禁止 demand 与 unified-gpus 同时使用。
- `amd/timing/memtrace` 目录当前不存在。旧分析器源码还在 `benchmark/replica-recovery/analyzer/`，但不能据此声称能在当前仓库直接产生 trace.meta 或 changed 掩码。

并行 ST 是当前所有者布局的九点 Jacobi 变体，与普通 stencil2d sample 的调度/布局不同，输出保留 workload 名和尺寸，不能隐藏改动后称为原样复现论文。
FW 在本轮小输入下可能没有 refill；不预设它一定有收益。

7 策略默认按 `norep → inv → retain → i1-no-s3 → i1` 形成逐步消融，另有 `i1-full-stale`、`i1-adaptive` 两个修复分支。
S4 没有独立端到端对照；PTE/TLB 元数据免费，实际缓存屏障计费。
adaptive 使用未来阶段读集，不是已实现的在线预测器。

普通平台 `stock/demand` 的 Driver kernel_time 不等于复制 Session 的 application_ps，不能混在同一张收益图里。
通用 core 的实际 kernel 次数和五项 refill 指标，须等对应 adapter/访问记录接好后才能完整得到；当前缺失项使用 null/空格，不能填零。

本次只增加脚本、配置和文档；保留现有 sample 与模拟器源码。后续若要接入其余应用，需单独验证共享张量地址、所有者布局、分区调度、跨阶段 host writes 和冲突回退，不能只在脚本里把策略名循环七遍。
