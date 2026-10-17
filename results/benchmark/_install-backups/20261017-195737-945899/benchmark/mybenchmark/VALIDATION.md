# kernel 内在线协议验证（2026-10-16）

默认核心应用已切换到 `core-kernel-internal-online-v1`。INV、retain、i1-adaptive 均使用运行中的实际请求；adaptive 完全在线，不读取未来计划。

本次核心批次：`/home/only/projects/mgpusim-uvm/results/benchmark/_suites/20261016-223949-0baab4`。12 个应用 × 3 策略 = 36 项成功，LeNet/VGG16 按用户要求跳过。
单 kernel 六项一致性验证：`/home/only/projects/mgpusim-uvm/results/benchmark/_kernel-internal-validation/20261016-224447-980724`。所有用例均执行四轮实际修改并检查整个页，四张物理 GPU 均有指令活动。

核心运行的全部 Go 源码哈希与最终安装源码匹配。各策略输入摘要、实际 home 一致；普通 demand 和显式历史 kernel 边界入口另行回归通过。

|应用|INV µs|retain µs|在线 adaptive µs|INV kernel 内 refill|adaptive kernel 内 repair|adaptive 干净副本读|adaptive 按需/全过期|
|---|---:|---:|---:|---:|---:|---:|---:|
|MM|53.479|53.479|53.479|0|0|0|0/0|
|C2D|667.781|667.560|899.286|23|106|30|106/0|
|ST|83.666|83.666|181.391|0|0|0|0/0|
|FIR|24.653|24.653|24.653|0|0|0|0/0|
|IM2COL|23.858|23.858|23.858|0|0|0|0/0|
|BFS|1903.331|1901.252|1563.503|749|237|574|82/187|
|SC|26.747|26.747|26.747|0|0|0|0/0|
|MT|654.017|654.017|654.017|0|0|0|0/0|
|FFT|49.341|49.341|49.341|0|0|0|0/0|
|PR|64.390|64.390|71.781|0|0|0|0/0|
|BS|1035.037|1035.037|3045.316|0|0|0|0/162|
|KM|118.020|117.981|117.981|0|0|0|0/3|

这些是小输入的模型执行结果。有改善也有退化；不能当作正式规模或实际 GPU 硬件性能。
当前单目录转换、全局排空及缓存控制会影响各策略时间；复制仍使用参数化服务和带宽模型，元数据处理免费。机制和限制见 [KERNEL_INTERNAL.md](KERNEL_INTERNAL.md)。

|单 kernel 用例|策略|kernel 内 refill|kernel 内 repair|修复后再次写失效|写期间干净副本读|
|---|---|---:|---:|---:|---:|
|repeated-write|inv|9|0|9|0|
|repeated-write|retain|0|9|9|0|
|repeated-write|i1-adaptive|0|9|9|0|
|clean-local-read|inv|12|0|9|0|
|clean-local-read|retain|0|12|9|0|
|clean-local-read|i1-adaptive|0|9|9|12|

开发检查：整个模块 `go build -p 2 ./...` 通过；复制/driver/localmapping/runner 与 Akita AT 的 race 回归通过；Python 回归 8 项通过。
有限容量 FIR 测试实际发生 90 次驱逐，总副本页峰值 3（每 GPU 上限一页），数值校验通过。

Windows 导出：`outputs/kernel-internal-validation/`；原始日志、SQLite 和全部统计保留在 Linux 各应用结果目录。旧模型验证说明保存在该导出的 `previous-docs/`。
