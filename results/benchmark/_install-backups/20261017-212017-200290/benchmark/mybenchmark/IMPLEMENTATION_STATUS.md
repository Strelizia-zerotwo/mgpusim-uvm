# 当前接入状态：kernel 内在线协议 v2

2026-10-17，源码：`/home/only/projects/mgpusim-uvm/mgpusim`。

默认核心策略为 INV、retain、i1-adaptive。v2 已去掉写触发的全局排空：写入仅更新目录元数据并沿正常数据路径写 home；读取触发按页维护，每 GPU 一个队列，不同 GPU 可并发。目标缓存使用运行中的 clean-range 失效；源数据通过真实 home L2 请求读取。

Adaptive 完全在线，只用过去实际读到的子块集合。逐子块版本检查覆盖 TLB 命中；部分修复不会掩盖其他旧子块。只在读侧清理和复用物理帧。容量不足可回退 home。

Kernel 边界保留实际缓存处理，并检查版本有效的副本字节。显式标注 system-scope 同步的应用对本 kernel 写过的页保守走 home；没有增加自动 fence 检测或原子指令实现。

LeNet/VGG16 继续默认跳过。FW 专用 adapter 仍属于历史 kernel 边界路径。PTE/TLB 和目录元数据处理仍免费；未声称独立 S4 或真实硬件性能。

实际验证与批次见 [VALIDATION.md](VALIDATION.md)，完整语义及运行教程见 [KERNEL_INTERNAL.md](KERNEL_INTERNAL.md)。v1 和更早结果保留，正式比较必须重跑同一 v2 路径的 INV。
