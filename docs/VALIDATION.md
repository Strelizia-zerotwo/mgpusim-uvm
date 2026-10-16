# 精简后 UVM 基线验证

日期：2026-10-16。执行环境：WSL 发行版 `only`，Go 1.27.1，golangci-lint 2.13.2。
源码：`/home/only/projects/mgpusim-uvm/mgpusim`。

## 保留与删除

保留 `-vm-mode=demand` 及其每 GPU GMMU、共享 HostMMU、映射安装、有限资源、请求合并和反压。
`-vm-mode=shared` 保留上游兼容路径及原有默认行为。
理想映射、早期理想页表、L3 TLB、LIBRA TLB 容量配置以及访存 trace/副本恢复扩展已删除。
映射存储包从 `idealmapping` 改名为 `localmapping`，已删除预装统一内存映射的策略。
普通分配仍预装，这是运行代码和内核参数的基线行为。

## 实际开发检查

以下命令已在当前源码上实际运行并通过：

```bash
cd /home/only/projects/mgpusim-uvm/mgpusim
export PATH=/usr/local/go/bin:/home/only/go/bin:$PATH
go build ./...
go test -race -p 2 ./amd/timing/localmapping ./amd/timing/faultvm ./amd/driver \
  ./amd/timing/cp ./amd/timing/cu ./amd/samples/runner/...
golangci-lint run ./... --timeout=10m

cd /home/only/projects/mgpusim-uvm/akita-patched
go test -race -p 2 ./mem/cache/writethroughcache ./mem/cache/writeback
```

Lint 最终输出 `0 issues.`。仅为本地 Akita 替换增加了一条精确匹配的
`gomoddirectives` 排除规则；其他依赖替换仍接受原有检查。

## 实际模拟结果

本轮输出目录：`/home/only/projects/mgpusim-uvm/results/uvm-validation-20261016-012442`。
运行入口：`bash experiments/fault-baseline/run.sh`（实际调用同目录 `run.py`）。
程序从精简后的源码重新编译，全部数值验证通过。

| 用例 | 数值验证 | 映射缺页数（全部 GPU 合计） | Driver kernel_time（µs） |
|---|---|---:|---:|
| FIR / 2 GPU / length=16384 / demand | Passed | 36 | 10.165 |
| FIR / 2 GPU / length=16384 / demand，主机 1 槽、队列 2 | Passed | 36 | 20.039 |
| FIR / 4 GPU / length=4096 / demand | Passed | 16 | 9.977 |
| SC / 4 GPU / 126×126 / demand | Passed | 39 | 10.927 |
| Bitonic Sort / 4 GPU / length=1024 / demand | Passed | 4 | 251.923 |

这些时间是当前模型输出，不能当作真实硬件测量。
双 GPU FIR 拥塞用例的平均主机队列等待为 765.389 ns，队列满 13,161 周期；
普通配置为 3.833 ns、0 周期。两者均为 36 次映射缺页。

检查还包括：

- 每张参与 GPU 都有 kernel 活动、TLB miss、本地遍历和映射缺页。
- 请求/响应、合并和缺页计数守恒，结束时没有遗留翻译或主机工作。
- 主机队列、服务槽及 GPU 在途事务均未超过配置容量。
- 主机服务时间及缺页最小延迟没有被绕过。
- 所有四 GPU demand 用例中，GPU 2–4 的远端请求都产生 RDMA outgoing 流量。
- SQLite 指标中没有 L3 TLB 组件。
- 4 GPU SC shared 62×62 和 2 GPU FIR 功能模拟 length=1024 也通过数值验证。
- 已移除的理想模式、L3 模式、旧理想页表和 TLB profile/L3 参数均明确拒绝。

`validation.json` 是机器可读结论；各 `*.log`、`*.sqlite3`、`commands.json` 和
`source-sha256.json` 保存运行证据。`summary.json` 记录两组 FIR 的对照。

## 正确性修复保留审计

清理前完整备份：`/home/only/projects/mgpusim-uvm-backups/before-cleanup-20261016-011813.tar.gz`。
SHA-256：`2e7899f98083abb60e7825c9f5a33cd82b8ee8a1bceabde704ef0f9f80b73e05`。

逐文件与备份比对确认：`akita-patched` 的 767 个文件、
Command Processor 的 39 个文件，以及驱动 `memorycopy.go` 和
`driver_test.go` 均字节一致，保留已有的远端路由、cache 控制和拷贝完成修复。
CU/驱动的变更删除的是可选访存记录器，保留 Akita 原有请求统计。
完整清理差异见本轮结果的 `cleanup.diff`，审计清单见 `cleanup-audit.json`。

验证范围是上述组件和程序规模；未宣称所有应用/规模均通过，也未实现迁移或页面复制协议。
