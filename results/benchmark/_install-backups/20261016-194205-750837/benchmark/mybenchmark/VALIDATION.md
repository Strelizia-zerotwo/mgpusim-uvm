# 本轮脚本验证（2026-10-16）

只做有限的小规模验证，未替用户完成正式核心集实验。

| 检查 | 实际结果 | 边界 |
|---|---|---|
| 参数核对 | 29 个 sample 目录、58 套 smoke/small 参数名与当前源码匹配 | 不表示所有输入数值或性能都已验证 |
| 脚本回归 | 5 项通过 | 检查多次写入合并、没有 refill 的空分布、缺失 home 拒绝、失败结果不计算收益、空 launch 不冒充 GPU 活动 |
| ST / policy / smoke | record + 7 策略均通过，四 GPU 每卡累计 30,880 条 CU 指令 | 258×258、2 轮、4 KiB、link=200 ns、fault=900 ns |
| FW / policy / smoke | record + INV/i1 通过，每卡约 2.7 万条 CU 指令 | 128 节点、8 个 pivot，不是完整 APSP，不是全 7 策略验证 |
| SC / stock / smoke | CPU 校验和真实四 GPU 活动通过 | 普通共享页表平台运行 |
| FIR / stock / small | CPU 校验和真实四 GPU 活动通过，每卡 100,352 条 CU 指令 | length=65536，taps=16 |
| C2D/FIR/BS/KM / stock / smoke | 数值或逐算子校验完成，但实际只有 GPU1 执行 CU 指令 | 已标记 INSUFFICIENT_GPU_ACTIVITY，不能作为四卡结果 |
| 合成机制记录 | 默认生成 80 个配置及机会总表 | 没有模拟器时序，也没有实测策略加速比 |

ST smoke 的 NoRep application=41.103 µs，INV/retain/i1-no-s3/i1/i1-full-stale/i1-adaptive 均为 42.763 µs。
FW smoke 的 INV 与 i1 application 均为 61.385 µs。当前这些配置没有证明差量策略的收益。
FIR small 的普通 Driver kernel_time=11.811 µs，宿主运行约 13.04 秒；这两个时间不可混淆。

验证结果位置：

- `results/benchmark/_suites/20261016-192326-50d253/`：ST 7 策略。
- `results/benchmark/_suites/20261016-192813-2bc58e/`：普通 sample smoke；活动状态已按 CU 指令重新汇总。
- `results/benchmark/_suites/20261016-193111-554b64/`：FW INV/i1。
- `results/benchmark/_suites/20261016-193540-544845/`：FIR small。
- `results/benchmark/synthetic/20261016-193158-c31be7/`：合成访问记录。

每组保留原始日志、SQLite、二进制与来源哈希。重新汇总的脚本版本记录在 `summary-provenance.json`。
Windows 可读的检查摘要在工作区 `outputs/benchmark-suite-validation/`。
