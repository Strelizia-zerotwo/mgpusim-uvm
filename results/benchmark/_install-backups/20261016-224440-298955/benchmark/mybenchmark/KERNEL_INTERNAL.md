# kernel 内在线复制协议

当前核心应用默认使用 `core-kernel-internal-online-v1`。INV、retain、i1-adaptive 都由 GPU 正在执行的实际读写请求触发。没有预先录制的读写计划，没有未来 kernel 写集，也不按 kernel 边界决定副本状态。

`i1-adaptive` 按用户要求改为完全在线：保留上一段实际读取的子块集合，估计按需修复可能导致的后续固定服务成本，与一次补齐全部过期子块的成本比较。没有历史时按需修复。这是历史预测策略，不能再称为前视到实际下一次写入的 oracle。预测失误只影响搬运量，不能绕过版本或权限检查。

## 三种策略

| 策略 | 实际读取 | 实际写入 |
|---|---|---|
| inv | 非 home 读第一次整页复制；被撤销后整页重新复制 | 撤销已有副本，释放副本帧；写入 home |
| retain | 首次整页复制；重新读取时补齐全部过期子块后恢复整页可读 | 撤销整个副本的读权限，保留帧和未改变的数据；写入 home |
| i1-adaptive | 所需子块仍有效时直接使用旧副本；过期时在线选择按需或全过期修复 | 对每次实际写请求更新版本和子块有效性；撤销受影响的访问；写入 home |

同一个 kernel 可以多次发生 `read → copy → write → revoke → read → repair → write → revoke`。首次写时撤销后，后续每一次写仍经过权限检查；修复不会让后续写入绕过失效。

## 请求、缓存和在途访问

1. 在向量和标量地址翻译器的实际请求入口检查权限，位置在数据缓存之前。TLB 命中也不能绕过检查。
2. 需要权限转换时暂缓新的 AT 请求，等待已经接受的请求完成。原始请求留在有限输入队列中，形成反压。
3. 当前实现一次处理一个目录转换，并保守地排空全局已接受请求。随后通过真实缓存控制消息排空数据 L1/L2，对相关 home/副本物理地址执行写回、失效和恢复。有限副本容量的受害帧也在复用前失效。
4. 写回确认后读取 home 的权威数据。复制使用原有参数化固定服务、链路延迟及有限带宽模型；功能字节真正写入对应 GPU 的副本物理帧。只有服务完成后才允许原读取请求继续。
5. 为每个在途请求保存物理帧权限，地址翻译输出使用该权限。直到数据响应送回上游才释放；权限转换不会回收在途请求使用的帧。

这是一个可执行的保守一致性模型。全局排空和单目录转换会限制并发，开销计入所有策略，**不能把其时间直接称为分布式硬件协议的性能**。PTE/TLB 元数据管理依然免费；每请求权限检查并未模拟一个有容量/延迟的元数据缓存。副本复制的源/目标 DRAM 排队未另行建模，复制服务仍是参数化模型。S4 的独立硬件收益尚未测量。

kernel 提交之间保留原样例的同步和缓存处理。kernel 边界还用于收集结果、处理 host I/O 和释放内存；它们不提供副本决策的未来信息。

## 运行

```bash
cd /home/only/projects/mgpusim-uvm
export PATH=/usr/local/go/bin:/home/only/go/bin:$PATH

# 先用小输入检查新路径。
python3 runs/run_benchmarks.py run \
  --mode policy --protocol kernel-internal \
  --bench FIR,BS --profile smoke --timeout 600

# 默认跳过用户允许跳过的 LeNet、VGG16，执行 12 个核心应用。
python3 runs/run_benchmarks.py run \
  --mode policy --protocol kernel-internal \
  --bench core --profile smoke --timeout 600

# 输入扩大时仍需看实际事件，不预设一定产生收益。
python3 runs/run_benchmarks.py run \
  --mode policy --protocol kernel-internal \
  --bench BFS,PR,KM --profile small --timeout 1800
```

`--policies inv,retain,i1-adaptive` 是默认值。`--mode record` 只执行在线 INV 并导出实际访问摘要，不作为未来计划输入后续策略。

历史结果可显式用 `--protocol kernel-barrier` 运行，标记为 `core-kernel-barrier-v1`，不能与新协议合并计算加速比。FW 的旧专用 adapter 属于历史 kernel 边界实验；当前核心在线 adapter 不包含 FW。

## 检查哪些计数

每个策略的 `stats.json` 包含 `passive`：

- `protocol` 必须为 `kernel-internal-online-v1`。
- `managed_requests/read_requests/write_requests` 来自实际请求准入。
- `intrakernel_refills/intrakernel_repairs`：同一副本在同一个 kernel 内已经复制过，失效后又恢复的次数。
- `write_after_repair_invalidations`：修复后再次实际写入而撤销副本的次数。
- `clean_replica_reads_during_write`：存在尚未转为普通读状态的写入页时，使用已有有效子块的本地读取。
- `adaptive_demand_repairs/adaptive_full_stale_repairs`：在线 adaptive 的两种选择。
- `events`：权限转换事件及其 kernel 编号、页、GPU、请求 ID、开始/完成时间、版本和复制字节。

`refills` 专指 INV 的整页重复制；retain/adaptive 需看 `repairs`。有复制但没有 refill/repair 仍可能是有效的负结果。

`initial_input_sha256` 和实际 home 放置用于检查各策略输入和所有者一致。各应用保留其原有的数值验证；不同策略可以产生不同的实际访存交错，尤其是 BFS，不再使用原先的 may-write 计划强制把可变页全部送回 home。

## 时间和结果

结果仍保存在 `results/benchmark/<应用>/<suite>/policy/`，总表在 `results/benchmark/_suites/<suite>/`，包含命令、日志、SQLite、TSV、JSON 和报告。

在线协议的 `kernel_us` 包含 kernel 内的权限等待、缓存处理和复制等待。`in_kernel_cache_control_us` 与 `in_kernel_copy_service_us` 是内部解释项，**不能再加到 application_us 上**。`recovery_us=0` 表示没有 kernel 外追加修复时间，不表示复制免费。

```text
application_ps = kernel_ps + barrier_flush_ps + host_transfer_ps
```

总表会显示 kernel 内恢复、修复后再写失效、写期间干净副本读和 adaptive 选择。`oracle-envelope` 只从已经完成的整次应用结果中离线选择，在线协议本身不使用它。

## 单 kernel 一致性验证

`mgpusim/amd/samples/replica-internal` 在四张 GPU 上执行同一个 kernel 的四个工作组：每个工作组对同一页中的独立数据范围反复读取、加一、写入，四轮之间没有 kernel 返回。CPU 检查整个页，包括应被修改的数据和应保持不变的部分。

`-clean-reads` 增加对同页未修改子块的读取，检查已有干净副本的本地读。指令流复用并校验仓库的 GCN3 copyKernel，再加入可反汇编检查的整数修改和等待指令，不依赖外部编译器。

它用于验证实际 kernel 内事件和一致性，不代表真实应用的性能收益。
