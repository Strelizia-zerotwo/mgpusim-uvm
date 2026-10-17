# 当前接入状态：kernel 内在线协议

2026-10-16，源码：`/home/only/projects/mgpusim-uvm/mgpusim`。

- 默认核心策略：INV、retain、i1-adaptive，运行时实际访存触发。
- 所有核心样例通过共同的请求权限控制入口；不使用提前录制的 kernel 读写集。
- adaptive 为用户指定的完全在线历史预测策略，已移除其对未来计划的依赖。
- 单 kernel 四轮真实读改写测试已通过三策略全数据校验，并要求出现 kernel 内恢复和恢复后的再次写失效。
- 同 kernel 的干净子块读取测试中，adaptive 实际发生 12 次写期间干净副本本地读取；INV/retain 为 0。
- 全局排空、串行目录转换是当前保守实现的明确限制；复制服务仍使用参数模型，不能当作分布式硬件性能。
- PTE/TLB 元数据免费，不声称独立 S4 收益。
- LeNet/VGG16 仍按用户要求默认跳过；没有新的在线训练验证声明。
- FW 专用入口仍是历史 kernel 边界实验，不属于本轮在线核心应用入口。

当前机制、命令、统计与限制见 [KERNEL_INTERNAL.md](KERNEL_INTERNAL.md)。验证批次见 [VALIDATION.md](VALIDATION.md)。

旧 `core-kernel-barrier-v1` 结果已保留，只能解释为历史模型检查，不能作为用户 kernel 内假设的实验验证。
