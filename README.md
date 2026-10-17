# MGPUSim UVM 映射缺页基线

后续工作目录：`/home/only/projects/mgpusim-uvm`。
Go 模块目录：`/home/only/projects/mgpusim-uvm/mgpusim`。

## 运行

```bash
cd /home/only/projects/mgpusim-uvm/mgpusim
export PATH=/usr/local/go/bin:/home/only/go/bin:$PATH
bash experiments/fault-baseline/run.sh
```

上面的入口重新编译并运行两/四 GPU 数值、缺页、有限队列、反压和远端 RDMA 检查。
输出保存在旁边的 `results/uvm-validation-<时间>/`，每轮使用独立目录。

单独运行 FIR：

```bash
cd /home/only/projects/mgpusim-uvm/mgpusim
/usr/local/go/bin/go build -o /tmp/mgpu-uvm-fir ./amd/samples/fir
/tmp/mgpu-uvm-fir -timing -verify -disable-rtm -gpus=1,2,3,4 \
  -use-unified-memory -vm-mode=demand -length=4096 -report-all \
  -metric-file-name=/tmp/mgpu-uvm-fir
```

## 当前模型

`-vm-mode=demand`：L1/L2 TLB → 每 GPU 本地 GMMU → 必要时共享 HostMMU → 本地安装映射。
统一内存页面的映射初始不在 GPU 本地页表；普通显存、代码和参数的映射仍预装。
同 GPU 同页请求合并；主机服务槽和队列有限，包含排队与反压。
数据位置固定，统一内存分配器把数据放在 GPU 1，其他 GPU 通过 RDMA 远端访问。
此模型不实现迁移、复制、显存驱逐、再次缺页或多级 PTE 的真实内存请求。

`-vm-mode=shared` 保留上游兼容路径，也是未指定 VM 模式时的默认值。
研究映射缺页时明确传入 `-timing -vm-mode=demand -use-unified-memory`，
通过 `-gpus` 选择独立 GPU，不与 `-unified-gpus` 混用。

早期理想映射模式、L3 TLB 和 LIBRA 容量配置仍不在本基线中。
新增的页面复制是独立的显式 oracle 实验入口，见下文。
原有可视化与 Akita 性能统计仍可使用。

## 保留的正确性修复

- 驱动在数据拷贝和全部 GPU cache flush 回复都完成后再完成拷贝命令，修复回复顺序导致的停滞。
- Command Processor 保留 cache drain/flush/invalidate 与 kernel 边界处理的修复。
- `akita-patched` 保留地址空间范围和远端端口，使 L1 对其他 GPU 内存的访问正确进入 RDMA。
- 通过 `localmapping` 的固定放置保护，拒绝开始翻译后的直接 remap/free，避免旧 TLB 映射。

`mgpusim/go.mod` 的本地 Akita 替换是这些修复的一部分；两个目录需要放在一起。

## 开发检查

```bash
cd /home/only/projects/mgpusim-uvm/mgpusim
export PATH=/usr/local/go/bin:/home/only/go/bin:$PATH
go build ./...
go test -race -p 2 ./amd/timing/localmapping ./amd/timing/faultvm ./amd/driver \
  ./amd/timing/cp ./amd/timing/cu ./amd/samples/runner/...
golangci-lint run ./... --timeout=10m
```

模型参数和指标见 `mgpusim/experiments/fault-baseline/README.md`。
本次验证记录见 `VALIDATION.md`。本目录当前不含 Git 元数据。

## 新增：INV 与理想差量修复

```bash
cd /home/only/projects/mgpusim-uvm
python3 benchmark/replica-recovery/run.py
```

详细操作：`docs/replica-implementation.md`。
基线配置单：`docs/INV-配置单.md`；理想配置单：`docs/I1-配置单.md`。
此入口执行两遍真实时序内核，使用真实物理副本和 per-GPU PTE，保留带宽/延迟和固定缺页开销。
它属于 kernel-barrier oracle 理想化机制评估；主 I-1 是受限的已有有效副本变体，不是普适数学上界，也不等于完整动态 UVM 复制协议。`-vm-mode=demand` 本身继续保持原有映射缺页含义。

## 审查修复 v2

页面复制的正式对照使用相同 oracle 映射准备的 NoRep / INV / 严格 I-1；新增写阶段冷预取独立变体和页级 home 回退。真实并发分区入口为 `parallel-stencil` 与 `parallel-stencil-overlap`，见 `docs/replica-implementation.md`。早期轮换 FW/stencil 的零收益、旧 NoRep 时间不可用作 idea 1 证据。

最终验证与结果索引：`docs/replica-validation.md`、`docs/replica-results.json`。本轮使用有限流水RDMA fabric；附加链路延迟需按文档扫描，不能当作已标定的硬件数值。

## 所有者放置与修复选择 v3

页面复制实验现默认按分区所有者做初始物理放置，并检查页对齐配置的远端写为0。新增i1-full-stale、i1-adaptive及包含INV的整次应用oracle选择。操作、配置和实际halo占比见docs/replica-implementation.md、docs/INV-配置单.md、docs/I1-配置单.md、docs/replica-validation.md。v2文档归档在docs/archive/replica-review-v2；旧布局结果保留用于回归，不能混作当前所有者基线
