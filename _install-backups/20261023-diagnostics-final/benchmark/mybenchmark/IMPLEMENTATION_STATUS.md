# 当前实现状态

2026-10-17，`kernel-internal-ideal-v3`。

已实现 `inv / delta / delta-sub / infinity`；2026-10-21起默认仅 `inv / infinity` 的 kernel 内实际请求触发；版本、失效、缓存维护与在途正确性全部免费。复制/修复仅收一次 fault 加总字节传输，输入映射免费预装。delta 每次补齐全部过期子块；delta-sub 额外允许干净子块直接读。infinity 是当前默认的复制免费参考，直接在kernel内同步完成功能复制，不收服务时间或链路带宽。

核心 14 个应用沿用通用 AT adapter，默认 12 个；LeNet/VGG16 根据用户要求跳过。本轮没有执行它们，不能从“接入、构建、单元测试通过”推出各应用已完成 ideal-v3 数值和性能验证。

脚本支持 4KB/64KB/2MB，900/20000ns fault，0/200ns 附加链路延迟的一轮扫描；保存每应用原始输出与关键数据摘要。旧配置和历史实验结果保留，但本轮不使用 retain/i1-adaptive、在线预测或 kernel 边界决策。

机制、功能假设与计时范围见 [KERNEL_INTERNAL.md](KERNEL_INTERNAL.md)；运行命令见 [RUNNING.md](RUNNING.md)。本轮验证记录见 [VALIDATION.md](VALIDATION.md)。

新增五个 C 类候选已接入同一理想实验：lud、gaussian、pathfinder、backprop、gramschmidt。它们使用当前仓库已有 CDNA3 内核，并由脚本选择 MI300X。详见 [IDEA_CANDIDATES.md](IDEA_CANDIDATES.md)。本轮未运行应用。

2026-10-19：新增推荐起步/目标预设和分档脚本。FW、NW标记接入kernel内入口；NW有编译内核功能对照和host侧修正，整应用timing未运行。见 [RECOMMENDED.md](RECOMMENDED.md)。

2026-10-22：新增5个CUDA来源研究移植版（SSSP、CC、COLOR、MIS、EMBEDDING），AMD gfx942内核及统一kernel内策略入口。详见 [cuda-ports/README.md](cuda-ports/README.md)。编译内核的功能测试通过；本次未执行应用timing或性能实验。
