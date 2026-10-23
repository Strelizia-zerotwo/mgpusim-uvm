# Kernel 内 INV / infinity：复制免费参考

2026-10-21。当前只运行 `inv,infinity`；四块 GPU，真实 kernel 内请求触发，沿用 ideal-v3。此前 infinity 的可选实现仍在，本次启用默认入口并补强测试。

2026-10-22 更新：`--bench` 也支持 C2D、gramschmidt、gaussian；专用入口与 `run_recommended.py` 的默认超时从每策略7200秒调整为86400秒（24小时）。

## 直接运行

在 WSL / Ubuntu 项目根目录：

```bash
cd /home/only/projects/mgpusim-uvm

# 仅查看配置，不构建、不运行。
python3 runs/run_infinity.py --dry-run

# BFS、NW、lud、FW × inv/infinity，共8次模拟；默认4KiB页。
python3 runs/run_infinity.py

# 同样四个应用，单独64KiB页，共8次模拟。
python3 runs/run_infinity.py --page-bytes 65536

# 或一次比较4KiB/64KiB，共16次模拟。
python3 runs/run_infinity.py --page-bytes 4096,65536

# 只选其中两个；逐个执行可避免多套模拟器争用主机资源。
python3 runs/run_infinity.py --bench NW,lud

# 此前超时的三个应用：依次执行，每个应用只跑 inv/infinity。
# 每次策略模拟默认最多24小时；仍沿用原 recommended-start 输入。
python3 runs/run_infinity.py --bench C2D,gramschmidt,gaussian

# 如需继续提高预算，例如 C2D 每策略最多48小时：
python3 runs/run_infinity.py --bench C2D --timeout 172800

# 2MiB页也支持；大页会改变页取整和home放置，需看分布。
python3 runs/run_infinity.py --page-bytes 2097152
```

默认 fault=900ns、link=200ns、bandwidth=300GB/s、四GPU、每配置一次，逻辑副本容量无限。`--timeout` 默认86400秒（24小时），按每次策略运行限制，构建另计；它限制宿主机等待时间，不改变模拟中的900ns固定服务开销。专用入口复用 run_recommended，可选择 BFS、NW、lud、FW、C2D、gramschmidt、gaussian；`--scale target`、`--bfs-graph`、`--lud-size`、`--scan` 等参数仍可用。完整扫描是每应用12参数点×2策略，共24次；四应用96次。

|应用|默认实际输入|
|---|---|
|BFS|`-node=262144 -degree=8 -depth=0`|
|NW|`-length=2048`|
|lud|`-size=512`，自动使用CDNA3/MI300X|
|FW|`-node=256 -iter=256`，完整APSP|
|C2D|`-N=16 -C=8 -H=64 -W=64 -output-channel=16 -enable-backward`|
|gramschmidt|`-m=2048 -n=128`，自动使用CDNA3/MI300X|
|gaussian|`-size=512`，自动使用CDNA3/MI300X|

C2D、gramschmidt、gaussian 在此前的7200秒预算下发生过超时。本次只延长运行预算，未重新执行应用，尚不能保证24小时内完成。多个应用放在同一条命令中会顺序执行；建议使用上述单套件命令，避免同时启动多套模拟器。

当前通用入口 `run_benchmarks.py`、`run_recommended.py`、`run_idea_candidates.py` 也统一默认 inv/infinity。原命令可继续使用，例如：

```bash
python3 runs/run_recommended.py --bench FW,NW
python3 runs/run_recommended.py --bench BFS,lud --page-bytes 65536
```

## 机制与校验

INV 写时立即失效并释放策略帧；后续读收一次固定服务费及整页传输。infinity 写时仍零时间更新版本、记录变化；后续读若没有本地副本则免费复制整页，若页版本过期则免费补齐全部过期子块，然后立即放行。功能操作真实发生，不读未来trace，不等kernel边界。

infinity 的固定开销、复制链路延迟和复制带宽均为零。计算、指令、普通缓存命中/未命中、普通访存、写home及其RDMA仍按原模拟器计时；infinity 不会使所有访问都变成L1命中。

自动检查 `charged_bytes=0`、`charged_fixed_us=0`、`in_kernel_copy_service_us=0`、`stale_read_sectors=0`、`mapping_faults=0`、四GPU活跃及应用数值校验。若物理帧不足导致退回远端home读，infinity 结果会被判为校验失败，避免把它误当本地复制免费参考。`copy_bytes` 和事件数仍记录实际功能复制，允许非零；整体fabric流量也可因普通RDMA而非零。

单元测试覆盖4KiB/64KiB/2MiB，首次复制、多个GPU读者、多次写后的全过期补齐、读干净子块时恢复页版本、修复后的再次写，无新增模拟时间或复制链路预留。本次没有运行任何benchmark。

## 输出与机会差值

结果仍在 `results/benchmark/<应用>/<suite>/policy/` 和 `results/benchmark/_suites/<suite>/`。原始MGPUSim日志、stats.json、SQLite、实际命令与校验状态保留；汇总新增：

- `saving_us_vs_inv`：`INV.application_us - policy.application_us`。
- `copy-free-reference.json/tsv`：只包含与同输入、同页大小、同参数的成功INV配对的成功infinity记录。
- `REPORT.md`：增加INV/infinity应用时间差和节省百分比表。

`INV − infinity` 是这两次运行之间的端到端复制免费参考差值。异步执行可能改变请求交错、合并与缓存状态，因此不宣称它是所有可能实现的严格数学上界，不将负值钳成0。只有未来同一完整对照组还运行delta时，才有数据计算 `(INV-delta)/(INV-infinity)`；本阶段只跑两策略，不虚构捕获率。

对64KiB或2MiB页，不假定差量字节仍等于4KiB实验里的几百字节，也不保证事件次数更少；页越大可能累积更多脏子块、改变共享和所有者放置。每个参数点内的INV/infinity采用相同输入与静态home规则。
