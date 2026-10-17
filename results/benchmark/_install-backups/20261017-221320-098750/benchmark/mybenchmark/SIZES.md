# 推荐输入与当前默认集合

核心名录 14 项，默认运行 12 项；LeNet/VGG16 按用户要求跳过。默认三策略为 inv、delta、delta-sub；当前为免费一致性的 kernel 内理想实验。
smoke 是首次正确性检查，small 是下一档；都不是论文原输入的精确复现。论文 footprint 原值见 PAPER_REFERENCE.md。

|类别|应用|smoke 参数|small 参数|当前说明|
|---|---|---|---|---|
|A|MM|`-x=64 -y=64 -z=64`|`-x=256 -y=256 -z=256`|通用三策略 adapter|
|A|C2D|`-N=1 -C=1 -H=16 -W=16 -output-channel=4 -enable-backward`|`-N=4 -C=3 -H=32 -W=32 -output-channel=8 -enable-backward`|通用三策略 adapter|
|A|ST|`-row=64 -col=64 -iter=2`|`-row=256 -col=256 -iter=4`|通用三策略 adapter；row/col 为内部尺寸，与旧 parallel-stencil 的258×258含halo布局分开|
|A|FIR|`-length=4096 -taps=16`|`-length=65536 -taps=16`|通用三策略 adapter|
|A|IM2COL|`-N=1 -C=1 -H=16 -W=16`|`-N=4 -C=3 -H=32 -W=32`|通用三策略 adapter|
|A|BFS|`-node=8192 -degree=4 -depth=4`|`-node=32768 -degree=4 -depth=8`|通用三策略 adapter；当前由实际请求处理，旧访问上界只用于历史 kernel-barrier|
|A|SC|`-width=126 -height=126 -mask-size=3`|`-width=510 -height=510 -mask-size=3`|通用三策略 adapter|
|A|MT|`-width=256`|`-width=512`|通用三策略 adapter|
|A|FFT|`-bytes=65536 -passes=1`|`-bytes=1048576 -passes=2`|通用三策略 adapter|
|A|PR|`-node=128 -sparsity=0.02 -iterations=2`|`-node=1024 -sparsity=0.01 -iterations=4`|通用三策略 adapter|
|A|BS|`-length=1024 -order-asc=true`|`-length=32768 -order-asc=true`|通用三策略 adapter|
|A|LeNet|`-epoch=1 -max-batch-per-epoch=1 -batch-size=1`|`-epoch=1 -max-batch-per-epoch=2 -batch-size=2`|默认跳过；显式 include-slow-dnn 才运行；合成自检输入|
|A|VGG16|`-epoch=1 -max-batch-per-epoch=1 -batch-size=1 -input-size=32`|`-epoch=1 -max-batch-per-epoch=2 -batch-size=1 -input-size=32`|默认跳过；显式 include-slow-dnn 才运行；合成自检输入；32×32 保留层次与通道，真实数据集走224×224|
|A|KM|`-points=1024 -features=8 -clusters=4 -max-iter=2`|`-points=16384 -features=16 -clusters=8 -max-iter=4`|通用三策略 adapter|
|B|SPMV|`-dim=256 -sparsity=0.02`|`-dim=2048 -sparsity=0.01`|补充 sample；不自动拥有通用三策略 adapter|
|B|FW|`-node=64 -iter=8`|`-node=128 -iter=16`|三策略走 parallel-fw，128节点，smoke 8 pivots / small 16 pivots；不是完整APSP|
|B|AES|`-length=4096`|`-length=65536`|补充 sample；不自动拥有通用三策略 adapter|
|B|MVT|`-size=64`|`-size=128`|补充 sample；不自动拥有通用三策略 adapter|
|B|3MM|`-size=64`|`-size=128`|补充 sample；不自动拥有通用三策略 adapter|
|B|GEMM|`-size=64`|`-size=128`|补充 sample；不自动拥有通用三策略 adapter|
|C|lud|`-size=32`|`-size=128`|补充 sample；不自动拥有通用三策略 adapter|
|C|gaussian|`-size=32`|`-size=64`|补充 sample；不自动拥有通用三策略 adapter|
|C|pathfinder|`-rows=16 -cols=128`|`-rows=64 -cols=512`|补充 sample；不自动拥有通用三策略 adapter|
|C|backprop|`-input=64 -hidden=32 -output=4`|`-input=256 -hidden=64 -output=4`|补充 sample；不自动拥有通用三策略 adapter|
|C|gramschmidt|`-m=32 -n=16`|`-m=128 -n=64`|补充 sample；不自动拥有通用三策略 adapter|
|C|hotspot|`-size=32 -iterations=2`|`-size=128 -iterations=4`|补充 sample；不自动拥有通用三策略 adapter|
|C|srad|`-size=32 -iterations=2`|`-size=128 -iterations=4`|补充 sample；不自动拥有通用三策略 adapter|
|C|jacobi2d|`-size=32 -tsteps=2`|`-size=128 -tsteps=4`|补充 sample；不自动拥有通用三策略 adapter|
|C|nbody|`-particles=256 -iter=1`|`-particles=1024 -iter=2`|补充 sample；不自动拥有通用三策略 adapter|

核心通用调度是均衡连续 workgroup 分区；不再依赖旧 unified 调度的64-CU粒度取整来决定 small 输入。每个新输入仍检查真实四卡 CU 指令。
所有者是每个分配的连续页区间静态归属，非严格 writer-owner；所有策略共用同一放置，并报告非home写与副本事件。
输入放大应逐项进行。可直接扫描4KB/64KB/2MB；大页搭配小输入时检查页面放置粗化。源码/参数已接入不等于每个 small 输入都已经实跑或已经展示收益。

本轮没有实跑应用。small 是建议起点，参数沿用上表；不声称这些小输入已有 ideal-v3 收益或代表论文原规模。
