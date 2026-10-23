# 七个新增 workload 的运行方法

名称：`SCC,PPR,SYMGS,PTA,LP,LOUVAIN,DMR`。独立分组 F，不改原来 14 个核心集。

默认比较四策略 `inv,infinity,delta,delta-sub`，默认 4KB 页、900ns fault、200ns link、4 GPU、CDNA3/MI300X。均接入现有 kernel 内 AT 请求路径。

请先阅读 [算法与访存边界](FIDELITY.md)：尤其 LP 为 colored in-place 变体，DMR 为 Delaunay 面积细化而不是 Chew 最小角度细化；PTA 和 Louvain 也有明确的数据结构或调度替代，不能当上游原版的性能复现。

```bash
cd /home/only/projects/mgpusim-uvm
export PATH=/usr/local/go/bin:/home/only/go/bin:$PATH

# 只检查参数，不构建、不执行模拟。
python3 runs/run_irregular.py --dry-run

# 建议先从这三项开始。每项依次运行四种策略。
python3 runs/run_irregular.py --bench SCC,PPR,SYMGS --timeout 7200

# 再运行 PTA 和社区标签传播。
python3 runs/run_irregular.py --bench PTA,LP --timeout 7200

# 这两项的辅助扫描/拓扑开销较多，建议独立运行。
python3 runs/run_irregular.py --bench LOUVAIN --timeout 7200
python3 runs/run_irregular.py --bench DMR --timeout 7200

# 一次列全 7 项 × 4 策略 = 28 次，按顺序执行。
python3 runs/run_irregular.py --timeout 7200

# 同样四策略改为 64KB 页；不要混入之前的 4KB 结果直接计算收益。
python3 runs/run_irregular.py --bench SCC,PPR,SYMGS --page-bytes 65536 --timeout 7200

# 先只筛 INV 与免费复制参考。
python3 runs/run_irregular.py --bench SCC,PPR --policies inv,infinity --timeout 7200
```

超时单位是每策略的宿主秒数；未显式指定时默认 86400。TIMEOUT 不自动意味着死锁。沿用 progress.jsonl、run.log 和自动保存的原生 Go 栈诊断。请避免同时运行多套吃内存的模拟器。

## small 起步规模

|名称|默认 small 参数|规模说明|
|---|---|---|
|SCC|`-node=4096 -degree=6 -components=8`|8 个强连通区，区间单向连接，再固定 seed 重编号；不是实测真实图|
|PPR|`-node=4096 -degree=6 -epsilon=0.0001`|单种子，alpha=0.15，保留全部 residual-copy 工作|
|SYMGS|`-node=4096 -degree=6 -sweeps=4`|4 个完整前后 sweep；着色对角占优矩阵|
|PTA|`-node=2048 -degree=2 -objects=64 -stores=16`|有限 Andersen 全四类约束，bitset 16KiB，另有约束数组|
|LP|`-node=4096 -degree=6`|按颜色调度、严格多数改善，运行到稳定|
|LOUVAIN|`-node=512 -degree=6 -components=4`|完整多层含 GPU 收缩；简单仲裁与排序可能昂贵，先保持小规模|
|DMR|`-points=16 -max-area=0.04 -max-insertions=128`|16 初始点含 4 个角点，达到面积条件即停止；至少 1024 拓扑容量槽|

所有输入默认 seed=42。`--profile smoke` 只是更小输入的可选档，本次没有运行该档或任何时序 smoke。

## 修改输入

统一使用已有 JSON 覆盖机制，不必改默认 catalog。例如：

```json
{
  "name": "irregular-larger",
  "benchmarks": {
    "SCC": ["-node=16384", "-degree=6", "-components=8", "-seed=42"],
    "PPR": ["-node=16384", "-degree=6", "-epsilon=0.0001", "-seed=42"],
    "SYMGS": ["-node=16384", "-degree=6", "-sweeps=4", "-seed=42"]
  }
}
```

保存后执行 `python3 runs/run_irregular.py --bench SCC,PPR,SYMGS --input-config /绝对路径/配置.json`。其他三项不要盲目放大：PTA 的对象数会改变 bitset 密度，Louvain 的扫描和排序增长明显，DMR 的几何范围与吞吐也不同。

## 结果

- 每应用：`results/benchmark/<名称>/<本轮ID>/policy/`。
- 总报告：`results/benchmark/_suites/<本轮ID>/REPORT.md`。
- 每策略保留 `run.log`、`stats.json`、`metrics.sqlite3`、`result.json`、`workload.json`、命令和进度。
- 汇总包含应用时间、kernel 时间、host 传输时间、宿主耗时、相对 INV 的节省、复制/修复次数与字节、失效次数、保留帧峰值、kernel 数和四 GPU 活动。
- 新增 scope、update semantics、implementation、完成标准、颜色数、层数、模块度、约束分类、三角形数和细化插入数。更详细的算法字段保留在 workload.json 与 result.json。
- INV/infinity 配对继续生成 copy-free-reference；delta/delta-sub 也分别与同配置 INV 比较。免费复制参考的收益不能代替真实差量策略的收益。

## 构建与测试

二进制 hsaco 随实现保存，正常运行只需 Go，不必重编 OpenCL。修改 kernel 后才需要本项目现有 LLVM 18 构建链：

```bash
python3 runs/build_irregular.py
cd mgpusim
go test ./amd/benchmarks/irregular -count=1 -timeout=150s
cd ../runs
python3 -B -m unittest test_irregular
```

单元测试执行小输入的编译后 AMD 指令与独立校验，不是带复制协议的四卡时序实验。正式时序和收益需要上面的运行脚本完成。
