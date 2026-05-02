# CKKS DAG 24/48 任务 Benchmark 报告

## 1. 报告概述

本报告面向 `Lattigo` 上实现的 CKKS 宽 DAG 任务，展示两类任务规模在不同执行模式下的 benchmark 结果：

- `24` 任务
- `48` 任务

每类任务都分别测试两种执行方式：

- `single-thread`：单线程顺序执行
- `manual-parallel`：手动并行执行

这个 benchmark 的目标，是展示同一类同态加密 DAG 任务在不同任务规模、不同执行策略下的性能表现。

## 2. 24 任务和 48 任务在做什么

这两类任务本质上都是在四个加密输入 `A`、`B`、`C`、`D` 上构造一个“宽 DAG”计算过程。

每一个 branch 都执行同样的计算模式：

1. 选择两个输入密文并相加。
2. 对相加结果做一次旋转。
3. 将旋转结果再加回去。
4. 对中间结果做平方。
5. 执行 `relinearize` 和 `rescale`。
6. 再做一次旋转。
7. 将旋转后的结果再加回去，得到该 branch 的输出。

当所有 branch 计算完成后，再执行一次尾部合并：

1. 将所有 branch 结果逐个相加。
2. 将合并结果与 `branch_00` 相乘。
3. 对乘法结果执行 `relinearize` 和 `rescale`。
4. 对结果执行一次 `rotate_32`。
5. 将旋转结果再加回去，得到最终输出。

因此，这个 benchmark 主要衡量的是以下几类 CKKS 操作在 DAG 场景下的代价：

- 密文加法
- 密文旋转
- 密文乘法
- relinearization
- rescale
- 多个独立 branch 的并行调度能力
- 整体 DAG evaluation 的端到端耗时

## 3. 24 任务与 48 任务的区别

### 24 任务

- branch 数量：`24`
- message shrink divisor：`64.0`
- 结构特点：24 个独立 branch，最后统一 merge tail

### 48 任务

- branch 数量：`48`
- message shrink divisor：`128.0`
- 结构特点：48 个独立 branch，最后统一 merge tail

可以把 48 任务理解为 24 任务的更宽版本。它拥有更多相互独立的 branch，因此更能体现并行执行带来的收益。

## 4. 两种执行模式说明

### 单线程模式

单线程模式下，所有 branch 按顺序依次计算，由一个 evaluator 串行完成。这个模式可以作为基线。

### 手动并行模式

手动并行模式下，每个独立 branch 会并行执行，等所有 branch 完成后再统一做 merge tail。这个模式用来体现 DAG 中“独立分支可并行”的特性。

## 5. 仓库中的实现位置

主入口：

- [benchmark_runner.py](/home/guoshuai/github/hmbench/benchmark_runner.py)
- [scripts/run_benchmarks.sh](/home/guoshuai/github/hmbench/scripts/run_benchmarks.sh)

核心 workload 实现：

- [lattigo/dagbench/wide.go](/home/guoshuai/github/hmbench/lattigo/dagbench/wide.go)
- [lattigo/dagbench/dag.go](/home/guoshuai/github/hmbench/lattigo/dagbench/dag.go)

四个 benchmark 程序入口：

- [lattigo/cmd/ckks_dag_single_thread_24_parallel/main.go](/home/guoshuai/github/hmbench/lattigo/cmd/ckks_dag_single_thread_24_parallel/main.go)
- [lattigo/cmd/ckks_dag_manual_parallel_24/main.go](/home/guoshuai/github/hmbench/lattigo/cmd/ckks_dag_manual_parallel_24/main.go)
- [lattigo/cmd/ckks_dag_single_thread_48_parallel/main.go](/home/guoshuai/github/hmbench/lattigo/cmd/ckks_dag_single_thread_48_parallel/main.go)
- [lattigo/cmd/ckks_dag_manual_parallel_48/main.go](/home/guoshuai/github/hmbench/lattigo/cmd/ckks_dag_manual_parallel_48/main.go)

## 6. 当前 benchmark 结果

结果来源：

- [output/lattigo_ckks_dag_24_48_benchmark_summary.txt](/home/guoshuai/github/hmbench/output/lattigo_ckks_dag_24_48_benchmark_summary.txt)

说明：

- 当前这份 summary 文件对应的是每种配置 `1 successful run` 的结果，因此它更适合描述为“当前样例结果”，还不能看作稳定均值。

### 6.1 关键结果表

| 配置 | DAG evaluation 时间 | Full pipeline 时间 |
|---|---:|---:|
| 24 任务单线程 | 26987.602 ms | 34373.866 ms |
| 24 任务手动并行 | 2525.402 ms | 9520.283 ms |
| 48 任务单线程 | 57261.725 ms | 64817.448 ms |
| 48 任务手动并行 | 3090.857 ms | 10283.276 ms |

### 6.2 加速比

以单线程为基线：

- 24 任务 `DAG evaluation` 加速比：`10.69x`
- 24 任务 `full pipeline` 加速比：`3.61x`
- 48 任务 `DAG evaluation` 加速比：`18.53x`
- 48 任务 `full pipeline` 加速比：`6.30x`

### 6.3 结果解读

- 对于 24 任务和 48 任务，`manual-parallel` 都显著降低了 `CKKS DAG evaluation` 的耗时。
- `evaluation` 阶段的提速通常明显大于 `full pipeline`，因为 `setup`、`key generation`、`encode`、`encrypt`、`decrypt/decode` 这些阶段并没有按同样比例缩短。
- 48 任务的并行收益高于 24 任务，这与它拥有更多独立 branch、可暴露更高并行度这一点一致。

## 7. 这份 benchmark 适合怎么对外介绍

如果需要给别人展示，建议这样描述：

> 我们构造了一个基于 CKKS 的宽 DAG benchmark，分别测试 24 个独立 branch 和 48 个独立 branch 的任务规模，并比较单线程顺序执行与手动并行执行两种模式的性能差异。

建议强调的几个重点是：

- 同一类 workload，不同 branch 数量：`24` vs `48`
- 同一类任务，不同执行方式：`single-thread` vs `manual-parallel`
- 核心对比指标：`CKKS DAG evaluation TIME`
- 辅助指标：`CKKS full pipeline TIME`

## 8. 对外展示前的建议

如果这份 benchmark 要用于正式汇报、论文、PPT 或对外对比，建议重新运行多次，使用平均值替代单次结果。

推荐命令：

```bash
bash scripts/run_benchmarks.sh
```

该脚本会将新的日志和 summary 结果输出到 `output/` 目录，并自动带时间戳，避免覆盖旧结果。
