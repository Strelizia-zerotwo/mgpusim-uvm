# 当前版本验证

旧的多模式对照记录已随清理前备份保存。当前版本的验证入口：

```bash
cd /home/only/projects/mgpusim-uvm/mgpusim
bash experiments/fault-baseline/run.sh
```

本次实际运行结果和开发检查见项目根目录 `VALIDATION.md`。
每轮原始日志、SQLite、源码哈希和机器可读结论在 `results/uvm-validation-<时间>/`。
这些结果验证固定放置映射缺页模型及所列规模的数值正确性，不代表硬件时延校准。
