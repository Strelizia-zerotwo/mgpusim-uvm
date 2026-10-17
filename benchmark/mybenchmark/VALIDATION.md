# ideal-v3 构建与测试记录

2026-10-17。当前协议 `kernel-internal-ideal-v3`，默认 `inv / delta / delta-sub`。

按用户要求，本轮没有执行 smoke、单 kernel 微基准、核心应用或完整扫描。下列证据只表明构建与测试通过；应用数值校验和收益需用户运行后确认。此前 v2 的 36 次应用结果成本口径不同，不作为本轮结论。

## 通过的检查

```bash
cd /home/only/projects/mgpusim-uvm/mgpusim
/usr/local/go/bin/go build -p 2 ./...
/usr/local/go/bin/go test -race \
  ./amd/timing/replica ./amd/timing/cp ./amd/driver/... \
  ./amd/timing/localmapping ./amd/samples/runner

cd /home/only/projects/mgpusim-uvm/akita-patched
/usr/local/go/bin/go build -p 2 ./...
/usr/local/go/bin/go test -race \
  ./mem/cache/writeback ./mem/cache/writethroughcache \
  ./mem/vm/addresstranslator

cd /home/only/projects/mgpusim-uvm
python3 -m unittest discover -s runs -p test_benchmark_tools.py -v
```

Python 脚本测试当前 16 项通过（包含五候选脚本、CDNA3 路由与架构分组新增测试）。Go 相关包通过；runner 无独立测试文件，通过其编译及全仓构建检查。Go race 测试包含仓库原有测试与新增回归测试。

## 新增机制回归覆盖

- INV 写时立即释放逻辑帧，delta 保留；多次写只对有效到失效计一次，所有一致性计时为零。
- 一次修复补齐多次写积累的全部过期子块；delta-sub 读干净子块不付固定服务，读脏子块时仍全部补齐。
- 等待期间又写入：原读保留首次检查时快照，后续读看新值；旧修复晚完成不能覆盖新修复。
- 同页/读者/版本读请求合并；不同页与不相交 GPU 对并发；同一 AT 中其他页可绕过等待项。
- 固定服务结束前不占用未来链路，普通流量可以继续使用链路；真实 SerialEngine 单元集成测试核对两个阶段事件及精确完成时刻。
- 失效帧的后端在途引用可免费完成；新分配的帧不因历史版本存在而提前可读。
- 帧回收后整页回退、分配失败回 home、H2D 权威内容重置、写入 byte mask。
- 4KB/64KB/2MB 均验证真实整页功能复制及页尾子块差量修复；3×2×2 扫描参数验证隔离服务费用顺序和一次固定费、一次 bulk 传输，无包头/区间费用。
- 免费缓存发布只写 dirty mask 指定的字节；地址范围外热行和在途锁/计数保留；不发送控制消息。
- 可选功能读响应钩子与完成回调；可选 CP 启动维护不排控制消息；无控制器的原有行为由既有测试回归。
- Python 验证默认仅三策略、36 次参数组合（模拟器调用使用替身）、页大小与协议独立匹配基线、失败排除、重汇总清理旧加速比、排序反转原样报告。

## 用户运行后需要确认

每次成功结果同时检查 benchmark 数值验证、四 GPU 实际 CU 指令、输入/home 一致、零映射缺页、零 stale_read_sectors、零收费一致性处理，以及字节/事件收费对应。

`functional_read_corrections` 是免费在途转发导致的后端值修正计数，可非零；它不能冒充硬件一致性验证。精确语义见 [KERNEL_INTERNAL.md](KERNEL_INTERNAL.md)。

当前没有 ideal-v3 应用耗时、速度提升或论文规模复现结论。


## 五个补充候选的追加验证

五个应用的 host 队列改为在选择目标设备后创建；MI300X 显式实验页大小不再被默认2MB覆盖；主机功能物化免费作废相关旧 MALL 行。kernel 算法和 gfx942 二进制沿用现有版本。

本次完成：

```bash
cd /home/only/projects/mgpusim-uvm/mgpusim
/usr/local/go/bin/go build -p 2 \
  ./amd/samples/rodinia_lud ./amd/samples/rodinia_gaussian \
  ./amd/samples/rodinia_pathfinder ./amd/samples/rodinia_backprop \
  ./amd/samples/polybench_gramschmidt
/usr/local/go/bin/go test -race \
  ./amd/benchmarks/rodinia/lud ./amd/benchmarks/rodinia/gaussian \
  ./amd/benchmarks/rodinia/pathfinder ./amd/benchmarks/rodinia/backprop \
  ./amd/benchmarks/polybench/gramschmidt
/usr/local/go/bin/go test -race \
  ./amd/samples/runner/timingconfig ./amd/timing/replica ./amd/samples/runner

cd /home/only/projects/mgpusim-uvm
python3 -m unittest discover -s runs -p test_benchmark_tools.py -v
python3 runs/run_idea_candidates.py --scan --dry-run
```

以上通过。五个 benchmark 的新增 Go 测试只构建轻量 driver、选择物理/统一设备、创建队列并加载已嵌入的内核符号，模拟时钟保持0；不执行 GPU 计算。平台测试含已有 MI300X 装配以及显式页大小覆盖测试。

本次没有运行五个应用、smoke 或扫描。实际四卡活动、应用数值正确性、耗时和收益仍由用户运行后验证。输入选择和算法边界见 [IDEA_CANDIDATES.md](IDEA_CANDIDATES.md)。
