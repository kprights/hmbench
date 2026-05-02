# hmbench

这个仓库现在只保留 Lattigo CKKS DAG 的 24 任务和 48 任务 benchmark。

保留内容：

- `24_ops_single_thread`
- `24_ops_manual_parallel`
- `48_ops_single_thread`
- `48_ops_manual_parallel`

主要入口：

- 根目录运行器：[benchmark_runner.py](/home/guoshuai/github/hmbench/benchmark_runner.py)
- 运行脚本：`scripts/`
- Go benchmark 程序目录：`lattigo/cmd/`
- benchmark 公共逻辑：`lattigo/dagbench/`
- 输出目录：`output/`
- 已有执行结果：
  - `output/lattigo_ckks_dag_24_48_run_<timestamp>.log`
  - `output/lattigo_ckks_dag_24_48_benchmark_summary_<timestamp>.txt`

运行方式：

```bash
bash scripts/run_benchmarks.sh
```

每次运行都会自动带上时间戳，避免覆盖旧结果。

只跑部分任务：

```bash
python3 benchmark_runner.py --only 24_ops_single_thread 48_ops_manual_parallel
```
