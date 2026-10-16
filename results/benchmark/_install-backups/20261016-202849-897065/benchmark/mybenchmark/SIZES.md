# 推荐输入：先跑通，再扩大

这不是论文输入的精确复现。论文大多只报告 footprint，没有提供能直接映射到当前 sample 的全部维度；参考原值见 PAPER_REFERENCE.md。
smoke 用于验证编译/参数/数值/GPU 活动，small 是第一轮机制检查。两档都刻意低于论文规模，不能据此概括正式性能。
目录内 29 个实际应用：A=14、B=6、C=9。D 合成记录由单独脚本生成。

|类别|应用|smoke 参数|建议第一轮 small 参数|说明|
|---|---|---|---|---|
|A|MM|`-x=64 -y=64 -z=64`|`-x=256 -y=256 -z=256`|立方计算；先不要直接上 1024³。|
|A|C2D|`-N=1 -C=1 -H=16 -W=16 -output-channel=4 -enable-backward`|`-N=4 -C=3 -H=32 -W=32 -output-channel=8 -enable-backward`|包含反向；底层会有 im2col/GEMM 等多个 kernel，不能按一个卷积算一个 kernel。当前仅平台检查。|
|A|ST|`-row=64 -col=64 -iter=2`|`-row=512 -col=512 -iter=4`|stock 的 row/col 不含 halo；policy 的 258×258 含 halo，pitch=272；small 做 4 轮。|
|A|FIR|`-length=4096 -taps=16`|`-length=65536 -taps=16`|长度是总样本数；16 taps。|
|A|IM2COL|`-N=1 -C=1 -H=16 -W=16`|`-N=4 -C=3 -H=32 -W=32`|3×3、stride=1、pad=0；输出会膨胀，不能只看输入张量。|
|A|BFS|`-node=1024 -degree=4 -depth=8`|`-node=262144 -degree=4 -depth=4`|随机图；depth 是截断深度，不表示完成整个连通分量。保留生成参数。|
|A|SC|`-width=126 -height=126 -mask-size=3`|`-width=510 -height=510 -mask-size=3`|sample 输入实际包含 mask halo；126/510 避免不合适的输出边界。|
|A|MT|`-width=128`|`-width=512`|方阵；比 MM 可更快扩大，但先检查四 GPU 活动。|
|A|FFT|`-bytes=65536 -passes=1`|`-bytes=1048576 -passes=2`|使用当前 sample 的 bytes 参数；每个 FFT 单元为 512 个复数。passes 不应直接解释成 kernel 数。|
|A|PR|`-node=128 -sparsity=0.02 -iterations=2`|`-node=1024 -sparsity=0.01 -iterations=4`|sparsity 是边密度；实际边数至少 node。四轮可能不是收敛结果，只是固定迭代实验。|
|A|BS|`-length=1024 -order-asc=true`|`-length=32768 -order-asc=true`|长度保持 2 的幂；多 pass，但不能预设有稀疏 dirty。|
|A|LeNet|`-epoch=1 -max-batch-per-epoch=2 -batch-size=1`|`-epoch=1 -max-batch-per-epoch=2 -batch-size=2`|保留每 GPU batch=1/2、两批以覆盖更新后再读；当前 UVM/Verify 阻塞，参数是接入后的起点。|
|A|VGG16|`-epoch=1 -max-batch-per-epoch=2 -batch-size=1`|`-epoch=1 -max-batch-per-epoch=2 -batch-size=1`|模型输入固定为 3×224×224；batch=1 仍然昂贵。当前 UVM/Verify 阻塞，不列为已跑通小任务。|
|A|KM|`-points=1024 -features=8 -clusters=4 -max-iter=2`|`-points=16384 -features=16 -clusters=8 -max-iter=4`|points/features/clusters/iter 同时固定；sample 的成功标记拼为 Passsed, RMSE，脚本已适配。|
|B|SPMV|`-dim=256 -sparsity=0.02`|`-dim=2048 -sparsity=0.01`|随机 CSR；报告 dim 和非零比例，不能只报告维度。|
|B|FW|`-node=64 -iter=8`|`-node=128 -iter=16`|stock smoke 64 节点；policy 两档均 128 节点（满足 4KB owner 对齐），分别 8/16 个 pivot，非完整 APSP。|
|B|AES|`-length=4096`|`-length=65536`|字节长度，保持 16 字节倍数。|
|B|MVT|`-size=64`|`-size=128`|CDNA3/MI300X 模型。|
|B|3MM|`-size=64`|`-size=128`|三个矩阵乘法，CDNA3/MI300X。|
|B|GEMM|`-size=64`|`-size=128`|CDNA3/MI300X；不要和 r9nano 的 MM 混作同硬件性能比较。|
|C|lud|`-size=32`|`-size=128`|N 保持 16 的倍数。|
|C|gaussian|`-size=32`|`-size=64`|N=64 先看数值验证，规模放大前观察模拟时间。|
|C|pathfinder|`-rows=16 -cols=128`|`-rows=64 -cols=512`|rows 控制 DP 阶段数，cols 控制每行数据量。|
|C|backprop|`-input=64 -hidden=32 -output=4`|`-input=256 -hidden=64 -output=4`|保留网络三维度；不承诺 small 一定形成跨 GPU 重读。|
|C|gramschmidt|`-m=32 -n=16`|`-m=128 -n=64`|分别报告 m/n，列方向写入是否分散需看实际访问。|
|C|hotspot|`-size=32 -iterations=2`|`-size=128 -iterations=4`|四轮仅界定机制事件，不表示达到稳态。|
|C|srad|`-size=32 -iterations=2`|`-size=128 -iterations=4`|四轮先校验数值。|
|C|jacobi2d|`-size=32 -tsteps=2`|`-size=128 -tsteps=4`|四个时间步；整页写是待验证预期。|
|C|nbody|`-particles=256 -iter=1`|`-particles=1024 -iter=2`|先 1 轮；2 轮 small 必须重新通过数值校验，历史 8 轮失败不能当成已修复。|

## 已接入 7 策略的两个应用

|应用|smoke|small|布局|
|---|---|---|---|
|ST / parallel-stencil|258×258，2 轮|258×258，4 轮|4GPU，每 GPU 64 内部行，padding 与页对齐，pitch=272 float|
|FW / parallel-fw|128 节点，8 pivots|128 节点，16 pivots|4GPU 行分区，两矩阵都是固定 owner；仅执行指定前缀迭代|

FW 的 128 是并行所有者版本的下限之一，不应改成 stock smoke 的 64 来套用所有者对齐假设。
第一轮不要直接扩大到论文 footprint，也不要把 ST/FW 的四卡每卡几十 KB 与论文 MB 级输入说成同规模。
扩大顺序建议：先保持 4KB 页与相同 link/fault/bandwidth，逐个放大输入至工作集有足够页和四卡活动，然后增加迭代数观察稳定 refill；最后再增加页大小与副本容量压力。
每个新输入都重新做 CPU 数值检查和实际 GPU 活动检查。单次 kernel 内机会不在本版 oracle 全局阶段模型覆盖范围内。
