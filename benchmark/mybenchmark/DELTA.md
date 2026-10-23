# 五个 -2 应用与 NW 的差量修复对照

2026-10-23。`SSSP-2、CC-2、COLOR-2、MIS-2、EMBEDDING-2、NW` 均接入相同的 kernel 内真实请求控制器，已有 `delta` 和 `delta-sub` 路径；不需要为每个 benchmark 另写协议。新增统一入口 `runs/run_delta.py`，默认运行四策略 `inv,delta,delta-sub,infinity`。

## 运行

```bash
cd /home/only/projects/mgpusim-uvm

# 检查全部配置，不构建、不启动模拟。
python3 runs/run_delta.py --dry-run

# 建议按批次依次运行，避免多套件争抢主机资源。
python3 runs/run_delta.py --bench SSSP-2,CC-2
python3 runs/run_delta.py --bench COLOR-2,MIS-2
python3 runs/run_delta.py --bench EMBEDDING-2,NW

# 或全部依次执行：6应用 × 4策略 = 24次模拟。
python3 runs/run_delta.py

# 保持相同输入比较 4KiB、64KiB：全部共48次模拟。
python3 runs/run_delta.py --page-bytes 4096,65536

# 可只选应用和页大小。
python3 runs/run_delta.py --bench NW --page-bytes 65536

# 只补跑两个差量策略；本套件没有INV行，不能自动给出相对INV的节省。
python3 runs/run_delta.py --bench SSSP-2,CC-2 --policies delta,delta-sub

# 修改每个策略的主机超时，或用自己的输入配置。
python3 runs/run_delta.py --bench NW --timeout 1800
python3 runs/run_delta.py --input-config /path/to/my-inputs.json
```

默认每策略主机预算86400秒，构建预算沿用通用脚本。主机超时不等于模拟死锁，也不等于模拟时间达到该数值。代理不自动启动本教程中的应用模拟。

## 输入与参数

默认 `small` 使用 `presets/delta-start.json`，与用户已完成的对应应用保持输入一致，避免 NW 意外回到 catalog 中的 length=256。

| 应用 | 默认输入 | 架构 |
| --- | --- | --- |
| SSSP-2 | 16384节点，degree=8，1分量，random，seed=42 | CDNA3/MI300X |
| CC-2 | 16384节点，degree=8，4分量，random，seed=42 | CDNA3/MI300X |
| COLOR-2、MIS-2 | 16384节点，degree=8，1分量，random，seed=42 | CDNA3/MI300X |
| EMBEDDING-2 | 8192行×32维FP32，global batch=128，8批，hotset，seed=42 | CDNA3/MI300X |
| NW | length=2048 | GCN3/R9Nano |

共同参数：四GPU、页4096B、子块32B、fault=900ns、link=200ns、带宽300GB/s、每策略一次。使用 `--page-bytes`、`--fault-ns`、`--link-ns`、`--bandwidth-gbs`、`--repeats` 覆盖。`--profile smoke` 选择 catalog 的更小输入，不使用 delta-start；只在用户主动指定时运行。

## 四种策略

| 策略 | 读触发的服务 | 固定/传输收费 |
| --- | --- | --- |
| inv | 写后释放副本，重读整页复制 | 每次900ns + 链路延迟/带宽及排队 |
| delta | 写后保留帧；重读修复副本版本以来全部过期32B子块 | 每次900ns + 差量传输及排队 |
| delta-sub | 只读未变化子块可直接读；需要修复时仍补齐全部过期子块 | 绕过修复不收费；触发修复时同delta |
| infinity | 实际读时同步、功能性地补齐正确数据 | 复制/修复固定服务、链路延迟和带宽占用全部免费 |

前三者首次无副本时按整页建立冷副本；帧被回收时也需整页回退。所有策略的协议元数据和一致性操作都免费，普通计算、缓存、访存和远端写正常计时。没有为 delta 另设更低的固定服务费，也没有 kernel 边界回放。

SSSP-2/CC-2 的单顶点写者、COLOR-2 的同轮颜色判断、MIS-2 的原地 mark/drop、Embedding-2 的 GPU 分组保持不变；来源与实现边界见 [cuda-ports/V2.md](cuda-ports/V2.md)。Embedding-2 的全局batch仍限1–256。

## 输出与解释

每个应用输出到 `results/benchmark/<ID>/<suite>/policy/<policy>_page-.../`。整批报告位于 `results/benchmark/_suites/<suite>/REPORT.md`，并有 JSON/TSV/CSV 汇总及复制免费参考表。

沿用现有输出：应用模拟时间及相对同配置INV的节省、主机耗时、冷复制/refill/repair次数、复制字节与收费字节、失效次数、保留帧占用、实际kernel/轮数、四GPU活动、数值和过期读取校验；保留原始日志、stats、progress、输入和源码指纹。

建议四策略同批运行。单独只跑delta/delta-sub也支持，但脚本不会自行从历史套件寻找INV来拼速度比。不同策略改变时序后事件数、收敛轮数可能不同，应结合这些数据解释总时间。累计固定服务时间与累计复制服务时间可重叠，不能直接当成应用停顿时间。

`INV-infinity` 是复制免费机会参考；`INV-delta`、`INV-delta-sub` 才是差量方案在当前计费下的实际模拟收益。新增脚本不代表六项delta/delta-sub的整应用时序结果已经实测通过。
