# 15项主实验集：32/64 GB/s INV与delta补跑计划

核对日期：2026-10-27。读取用户附上的结果汇总，并从当前WSL原始结果重新审计。没有启动模拟实验。

## 缺项结论

按本轮明确的大输入、4KB页、900ns fault、200ns link，已经完成32/64 GB/s两档INV/delta配对的是COLOR-2、MIS-2（65536节点）和NW（length3072）。其余12应用缺少这组大输入对照，共24配对、48次模拟。
历史16384节点图或NW2048的完成结果不会替代本轮大输入；C2D小输入完成也不算大输入完成。

|应用|固定的大输入参数|32 GB/s INV+delta|64 GB/s INV+delta|
|---|---|---|---|
|BFS|`-node=262144 -degree=8 -depth=0`|待补|待补|
|SSSP-2|`-node=65536 -degree=8 -components=1 -graph=random -seed=42 -max-rounds=4096`|待补|待补|
|CC-2|`-node=65536 -degree=8 -components=4 -graph=random -seed=42 -max-rounds=4096`|待补|待补|
|COLOR-2|`-node=65536 -degree=8 -components=1 -graph=random -seed=42 -max-rounds=4096`|已完成，跳过|已完成，跳过|
|MIS-2|`-node=65536 -degree=8 -components=1 -graph=random -seed=42 -max-rounds=4096`|已完成，跳过|已完成，跳过|
|PR|`-node=4096 -sparsity=0.0025 -iterations=8`|待补|待补|
|KM|`-points=65536 -features=16 -clusters=8 -max-iter=8`|待补|待补|
|NW|`-length=3072`|已完成，跳过|已完成，跳过|
|lud|`-size=512`|待补|待补|
|pathfinder|`-rows=64 -cols=4096`|待补|待补|
|C2D|`-N=16 -C=8 -H=64 -W=64 -output-channel=16 -enable-backward`|待补|待补|
|EMBEDDING-2|`-rows=32768 -dim=32 -batch=256 -batches=16 -distribution=hotset -seed=42`|待补|待补|
|MM|`-x=512 -y=512 -z=512`|待补|待补|
|ST|`-row=1024 -col=1024 -iter=8`|待补|待补|
|IM2COL|`-N=16 -C=8 -H=64 -W=64`|待补|待补|

## 输入的依据

“大输入”按每应用固定，不表示所有应用字节数相等。底层命令中的--profile small只是输入槽位名称，15项实际参数全部由main15-large.json显式覆盖，不会回落到catalog的小输入。BFS262144、LUD512沿用已测较大规模；COLOR/MIS65536与NW3072沿用已经完成的放大配置；SSSP/CC统一扩到65536节点。PR、KM、pathfinder、Embedding、MM、ST、IM2COL采用本轮明确提出的放大输入，尚未宣称成功运行或代表论文规模。
C2D沿用历史大输入N16/C8/H64/W64/output16并启用backward；这档曾超时/未完成，本轮使用每策略86400秒预算也不保证结束。脚本先做完本组其他应用的两档带宽，再运行C2D。

## 三个终端

每个终端先进入/home/only/projects/mgpusim-uvm。然后分别执行：
```bash
python3 runs/run_main15.py --group 1
```
```bash
python3 runs/run_main15.py --group 2
```
```bash
python3 runs/run_main15.py --group 3
```

|终端|当前实际待跑应用|已完成自动跳过|当前模拟次数|
|---|---|---|---:|
|1|BFS、SSSP-2、PR、MM|COLOR-2|16|
|2|CC-2、KM、ST、IM2COL|MIS-2|16|
|3|lud、pathfinder、EMBEDDING-2、C2D|NW|16|

三组应用不重叠。各进程顺序执行自己的任务，INV/delta在同一新套件配对。三组仍可能有不同耗时，16次相等不代表完成时间相等。

## 参数及续跑

默认带宽仅32/64，策略仅inv/delta，page4096，fault900，link200，每策略一次，当前平台每卡4GiB，未设置副本页数上限；没有加入上次讨论但尚未实现的每卡总显存2GiB。模拟器和benchmark代码未修改。
```bash
# 仅查看实际缺项与命令，不构建、不运行。
python3 runs/run_main15.py --group 1 --dry-run
# 中断后重新运行原命令；已形成合格配对的案例会跳过。
python3 runs/run_main15.py --group 1
# 只补单个应用/带宽。
python3 runs/run_main15.py --bench SSSP-2 --bandwidths 64
# 显式全部重跑，才使用此选项。
python3 runs/run_main15.py --group 1 --rerun
```
每策略默认宿主超时86400秒，可加--timeout 172800等覆盖。TIMEOUT/未完成/数值失败不能跳过；只完成INV时会把INV与delta一起重跑，保证新对照同批完成。
跳过检查要求当前协议、实际大输入、架构、四GPU活动、实际配置、最终result/stats/process成功和数值PASSED；还要求初始化内容、home分布与非策略配置一致。跨批配对额外比较binary与replica源码指纹。历史对照是其保存版本的结果，不宣称与将来任意修改后的模拟器等价；修改算法/协议后应显式--rerun。

## 结果

应用结果仍在results/benchmark/<应用>/<新批次>/policy/；每次子套件打印REPORT路径，保存完整命令、输入配置、源码指纹、原始日志、stats、metrics与应用时间/字节/次数摘要。并行终端运行应比较application_us，不把宿主wall_s当作模拟性能。
跑完可执行python3 runs/summarize_results.py刷新总览。

## 验证边界

37个Python测试与三个分组及全组dry-run均已通过。只执行单元测试与dry-run，未替用户启动正式实验或smoke。已审计的源记录在audit-data.json，缺项配对证据在coverage.json。
