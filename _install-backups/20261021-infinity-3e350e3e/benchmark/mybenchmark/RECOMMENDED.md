# 推荐工作负载：起步与目标规模

2026-10-19。按用户给定推荐表配置；默认 inv、delta、delta-sub，四块独立 GPU，kernel 内实际 AT 请求触发。协议开销按现有 ideal-v3 口径：免费一致性，复制/修复收一次 fault 和总字节传输。没有离线未来访问回放。

## 输入预设

|档次|ID|sample|start 起步|target 目标|
|---|---|---|---|---|
|第一档|BFS|bfs|`-node=262144 -degree=8 -depth=0`|`-node=1048576 -degree=8 -depth=0`|
|第一档|FW|floydwarshall|`-node=256 -iter=256`|`-node=512 -iter=256`|
|第一档|gaussian|rodinia_gaussian|`-size=512`|`-size=1024`|
|第一档|lud|rodinia_lud|`-size=512`|`-size=1024`；可覆盖为2048|
|第二档|gramschmidt|polybench_gramschmidt|`-m=2048 -n=128`|相同（用户只给了一档）|
|第二档|C2D|conv2d|`-N=16 -C=8 -H=64 -W=64 -output-channel=16 -enable-backward`|相同（用户只给了一档）|
|第二档|NW|nw|`-length=2048`|相同（用户只给了一档）|

目标规模来自用户的推荐，不代表已核对并复现某篇论文的原始配置。可编辑 `presets/recommended-start.json`、`presets/recommended-target.json` 后重跑；旧 smoke/small 配置保留，结果中的 profile 列会显示推荐预设名称和实际 input_flags、input_sha256。

BFS 的 depth=0 表示不截断层数。FW 起步是256次完整 pass；目标为512节点但仅256次 pass，是部分 APSP，不能标成完整512次。Gaussian 实际 kernel 数为 `2*(N-1)`，分别1022和2046；不是精确2N。

## 直接执行

```bash
cd /home/only/projects/mgpusim-uvm
export PATH=/usr/local/go/bin:/home/only/go/bin:$PATH

# 只看参数，不构建/运行。
python3 runs/run_recommended.py --dry-run

# 默认：第一档四个应用 × 三策略，起步规模。
python3 runs/run_recommended.py

# 单个应用，或任意子集。
python3 runs/run_recommended.py --bench FW
python3 runs/run_recommended.py --bench BFS,lud

# 第二档；或者全部七个。
python3 runs/run_recommended.py --tier second
python3 runs/run_recommended.py --tier all

# 第一档目标规模。
python3 runs/run_recommended.py --scale target

# LUD单独使用2048。
python3 runs/run_recommended.py --scale target --bench lud --lud-size 2048

# 每次模拟的宿主超时；默认7200秒，构建超时单独控制。
python3 runs/run_recommended.py --bench gaussian --timeout 14400
```

这些规模远大于以前 small。上述命令会运行实验，本轮实现过程中没有执行它们。超时是每次策略模拟的宿主上限，实际时长尚未测量。

## 页大小与服务参数扫描

```bash
# 单独64KB页。
python3 runs/run_recommended.py --page-bytes 65536

# 3页大小 × 2 fault × 2 link，每应用36次模拟。
python3 runs/run_recommended.py --scan

# 只扫描FW，或全部应用的目标规模。
python3 runs/run_recommended.py --bench FW --scan
python3 runs/run_recommended.py --tier all --scale target --scan
```

完整扫描参数为4096/65536/2097152字节页、900/20000ns fault、0/200ns附加链路延迟。四应用扫描共144次策略模拟，七应用共252次。脚本每个 sample 构建一次。

## 真实BFS图

```bash
python3 runs/run_recommended.py --bench BFS \
  --bfs-graph /absolute/path/road-network.txt
```

输入格式沿用原 sample：文本有向边列表，每行 `源节点 目标节点`，`#` 注释。脚本会去掉 node、degree 和已有 load-graph 参数，加入实际文件路径与 depth=0，避免原 sample 对参数冲突的报错。保留文件SHA256，并参与输入分组；同一条指令仍运行三策略。图加载器格式、顶点编号和可达性以现有BFS实现为准，未提供或下载道路网数据。

## 统一脚本入口

专用脚本转交现有统一 runner，也可以显式执行：

```bash
python3 runs/run_benchmarks.py run \
  --mode policy --protocol kernel-internal --profile small \
  --bench BFS,FW,gaussian,lud \
  --input-config benchmark/mybenchmark/presets/recommended-start.json \
  --policies inv,delta,delta-sub --timeout 7200
```

预设只替换相关应用输入，不改变GPU调度、协议、校验或核心名录。FW现已标记core_replica，走真实请求触发的ideal入口。其8×8工作组按线性连续区间分给四卡，对上述尺寸恰好等价于之前 ParallelBenchmark 的连续输出行分区，保留原条件写内核；不轮流用单卡执行各pass，也不使用历史阶段回放。

## NW修正与验证边界

NW现接入相同统一四卡路径。修正了：核心分配后仍调用 Distribute 破坏固定物理home、GCN kernarg未完整覆盖120B ABI尾部、非法非64倍数长度被静默截断，以及成功校验缺少Passed标记。输入使用局部固定随机源，跨策略可复现。

GCN3和CDNA3的已编译两个波前内核已加入直接功能单元测试，逐格比较CPU动态规划，覆盖上半和下半波前；另测完整参数ABI和核心模式保持分配放置。它们是内核功能测试，不包含整应用 timing 缓存路径。此前“单GPU失败”的旧结果不能直接当成本轮修复验证；本轮未运行普通单卡或四卡 timing应用，故不能宣称旧端到端失败已经复现并消除。NW放在第二档，实际运行必须通过原数值Verify及当前协议检查；失败结果不会进入收益统计。

## 输出与判读

每应用目录：`results/benchmark/<ID>/<本轮ID>/policy/`。
总目录：`results/benchmark/_suites/<本轮ID>/`。

保留日志、命令、资源统计、SQLite、metrics.tsv、stats.json、实际访问摘要和每应用/整批JSON/TSV/CSV/REPORT.md。预设另复制为套件中的input-config.json；benchmark.json保存有效参数，source-sha256包含新脚本和预设。汇总有应用/kernel/host时间、相对INV节省、复制/修复次数和字节、失效、保留帧峰值，以及实际kernel_count。

同组INV需匹配架构、页大小、链路、固定服务、容量预算及输入哈希，失败/超时剔除。应用收益仍需实测；本表中的模式解释和先前C2D 3.3%数字来自用户背景，未用作本轮测量结论。
