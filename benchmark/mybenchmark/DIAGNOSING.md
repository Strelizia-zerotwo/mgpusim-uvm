# 进度、停滞与失败诊断

2026-10-23。当前 ideal kernel 内路径自动记录进度，默认每30秒一次，并在每个kernel开始/结束时立即记录。只增加宿主诊断，不推进模拟时间、不安排额外模拟事件、不改变复制/失效规则。

每次策略目录包含：

- `run.log`：`CORE_PROGRESS` 行包含kernel编号、是否仍执行、最近采样模拟时间、请求数、引擎状态及Go堆内存。
- `progress.jsonl`：对应结构化记录，包括复制次数/字节、等待读取、在途修复、GC次数和协程数。请求快照约每1024个管理请求更新一次，kernel开始/结束时刷新。
- `progress.jsonl.stacks.txt`：连续周期采样中，kernel未结束、引擎已停止、快照未改变时，自动用进程内 `runtime.Stack` 保存协程栈；不会杀死或重新启动进程。这是诊断记录，不直接作为算法死锁判定。

## 先用小输入定位

```bash
cd /home/only/projects/mgpusim-uvm

# 依次执行：每个应用两策略，每次策略最多5分钟。
python3 runs/run_infinity.py --bench gramschmidt,gaussian,C2D \
  --input-config benchmark/mybenchmark/presets/diagnostic-small.json \
  --timeout 300
```

诊断规模为Gaussian32、Gramschmidt1024×4、带反向C2D N4/C3/H32/W32/O8。它们只用于定位推进或等待，不能和原推荐大输入拼接计算收益。正式默认输入没有缩小。需要时可一次只选一个应用。

启动后，从终端的SUITE找到本轮ID，在另一终端查看：

```bash
tail -f results/benchmark/gramschmidt/<本轮ID>/policy/inv_page-4096_link-200ns_fault-900ns_repeat-0/progress.jsonl
```

若请求数和模拟时间持续增长，说明采样点确实在推进；CPU很低也可能只是在看Python等待进程或把单核负载除以整机核心数。若正在kernel中、引擎停止、计数长期不动，则需要结合自动保存的栈定位丢失事件。引擎忙但计数不变还可能在计算、GC或其他组件，不能单凭这一项判定死锁。

查看宿主资源时要看实际模拟器进程，而非 `python3` 或 `/usr/bin/time` 包装进程。使用 `ps` 的100%表示占满一个逻辑CPU；32线程机器上的约140%可能只显示为整机约4.4%。Swap已占用不等于当前持续换页，应同时看内存/IO pressure和采样差值。

## 内核资源修正

2026-10-23的SSSP/CC/COLOR失败日志显示 `faultvm: no usable authoritative allocation`。根因是新LLVM生成的gfx942代码未导出当前MGPUSim加载器读取的寄存器资源符号，使这些内核只分配8个SGPR。构建脚本现从编译器自身的描述提取资源数并导出ELF符号；四个内核的指令字节与修复前完全一致，SGPR分配依次修正为40、24、16、32。新增回归测试覆盖该编译器到timing模型的接口。

重新运行 `python3 runs/run_cuda_ports.py --bench SSSP,CC,COLOR` 会创建新套件并从当前源码构建sample。旧套件里的已构建二进制不会自动更新，正在运行的旧实验也不会自动获得进度记录。

## 已有证据的边界

此次检查中的Gaussian进程在77秒采样区间消耗约136.9%的单核CPU，说明进程活跃，尚未证明其模拟时间一直前进。Gramschmidt INV长时间停在第一个kernel的队列完成等待，取栈时没有运行中的引擎协程，支持等待停滞；尚未定位丢失事件的具体组件。该INV随后在GDB协程取栈后SIGILL退出，可能受调试干扰，该次退出不能用作原始模拟器故障证据。后续只使用进程内安全诊断。

`--bench gramschmidt,C2D` 按应用顺序执行；当Gramschmidt未结束时，同一套件的C2D还没有开始。这与此前C2D已完成若干kernel后达到7200秒预算是不同情况。

本次修复验证为单元测试、资源元数据检查与sample构建；未运行新的timing应用或smoke。Gramschmidt的等待停滞尚不能宣称已修复。
