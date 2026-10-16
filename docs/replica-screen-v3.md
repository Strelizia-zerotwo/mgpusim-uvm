# 局部写与再读筛选

当前可重算的输入是replica sample录制的observed-plan.json和真实page_ownership；不依赖已缺失的历史bin trace。

结果：results/replica-screen-v3/screen.json。parallel-fw(128,12轮)有真实四GPU活动和条件写，但跨GPUread-refill=0，未进入策略性能回放。owner Jacobi有12次read-refill，但全部过期子块占页97.265625%，属于弱S1候选。两者都不能作为稀疏写入且反复恢复副本的正面证据。

记录中的written掩码按32B子块统计；不是“最终数值发生改变”的掩码。screen输出是访问机会统计，不预测总时间。后续BFS/SSSP/pagerank-delta/embedding必须先有正确的多GPU分区实现和数值校验，再用相同筛选口径进入timing对照。
