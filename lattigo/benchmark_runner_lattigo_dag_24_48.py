#!/usr/bin/env python3
"""
Run the Lattigo 24/48-branch CKKS DAG examples repeatedly and report average timings.

Default programs:
  - ./cmd/ckks_dag_single_thread_24_parallel
  - ./cmd/ckks_dag_manual_parallel_24
  - ./cmd/ckks_dag_single_thread_48_parallel
  - ./cmd/ckks_dag_manual_parallel_48
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from collections import defaultdict
from pathlib import Path
from typing import DefaultDict, Dict, Iterable, List, Sequence, Tuple


DEFAULT_RUNS = 10
DEFAULT_TIMEOUT_SECONDS = 1200
DEFAULT_OUTPUT = Path("lattigo_ckks_dag_24_48_benchmark_summary.txt")

PROGRAMS: Sequence[Tuple[str, str]] = (
    ("24_ops_single_thread", "./cmd/ckks_dag_single_thread_24_parallel"),
    ("24_ops_manual_parallel", "./cmd/ckks_dag_manual_parallel_24"),
    ("48_ops_single_thread", "./cmd/ckks_dag_single_thread_48_parallel"),
    ("48_ops_manual_parallel", "./cmd/ckks_dag_manual_parallel_48"),
)

TOP_LEVEL_SUFFIX_ORDER = [
    "CKKS setup",
    "CKKS key generation",
    "CKKS runtime object setup",
    "Message preparation",
    "Plaintext reference",
    "CKKS encode",
    "CKKS encrypt",
    "CKKS DAG evaluation",
    "CKKS decrypt/decode",
    "CKKS full pipeline",
    "example total",
]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Benchmark Lattigo 24/48-branch CKKS DAG single-thread and manual-parallel examples."
    )
    parser.add_argument(
        "--runs",
        type=int,
        default=DEFAULT_RUNS,
        help=f"Number of successful runs per program. Default: {DEFAULT_RUNS}.",
    )
    parser.add_argument(
        "--timeout",
        type=int,
        default=DEFAULT_TIMEOUT_SECONDS,
        help=f"Timeout in seconds for each single program run. Default: {DEFAULT_TIMEOUT_SECONDS}.",
    )
    parser.add_argument(
        "--output",
        type=Path,
        default=DEFAULT_OUTPUT,
        help=f"Summary output file. Default: {DEFAULT_OUTPUT}.",
    )
    parser.add_argument(
        "--go",
        default="go",
        help="Go executable to use. Default: go.",
    )
    parser.add_argument(
        "--only",
        choices=[label for label, _ in PROGRAMS],
        nargs="+",
        help="Run only selected benchmark labels.",
    )
    return parser.parse_args()


def average(values: Iterable[float]) -> float:
    values = list(values)
    if not values:
        return 0.0
    return sum(values) / len(values)


def metric_sort_key(name: str) -> Tuple[int, str]:
    for index, suffix in enumerate(TOP_LEVEL_SUFFIX_ORDER):
        if name.endswith(suffix):
            return index, name
    if name.startswith("parallel_") and name.endswith("TOTAL_TIME"):
        return len(TOP_LEVEL_SUFFIX_ORDER), name
    if name.startswith("branch_"):
        return len(TOP_LEVEL_SUFFIX_ORDER) + 1, name
    if name == "merge_tail":
        return len(TOP_LEVEL_SUFFIX_ORDER) + 2, name
    if name.startswith("["):
        return len(TOP_LEVEL_SUFFIX_ORDER) + 3, name
    return len(TOP_LEVEL_SUFFIX_ORDER) + 4, name


def parse_timing_output(output: str) -> Dict[str, float]:
    timings: Dict[str, float] = {}
    time_pattern = re.compile(
        r"^\s*(?P<name>.+?)\s+(?P<kind>TIME|TOTAL_TIME):\s+"
        r"(?P<value>[0-9]+(?:\.[0-9]+)?)\s+ms(?:\s+\|.*)?\s*$"
    )

    for line in output.splitlines():
        match = time_pattern.match(line)
        if not match:
            continue

        name = match.group("name").strip()
        kind = match.group("kind")
        if kind == "TOTAL_TIME":
            name = f"{name} TOTAL_TIME"
        timings[name] = float(match.group("value"))

    return timings


def run_once(go_bin: str, package: str, timeout: int) -> Dict[str, float]:
    result = subprocess.run(
        [go_bin, "run", package],
        capture_output=True,
        text=True,
        timeout=timeout,
    )

    if result.returncode != 0:
        raise RuntimeError(
            f"program exited with code {result.returncode}\n"
            f"stdout:\n{result.stdout}\n"
            f"stderr:\n{result.stderr}"
        )

    timings = parse_timing_output(result.stdout)
    if not timings:
        raise RuntimeError("no timing data found in program output")
    return timings


def run_program(label: str, go_bin: str, package: str, runs: int, timeout: int) -> List[Dict[str, float]]:
    results: List[Dict[str, float]] = []

    print("\n" + "=" * 90)
    print(f"Running {label}: go run {package} ({runs} successful runs requested)")
    print("=" * 90)

    attempts = 0
    max_attempts = runs
    while len(results) < runs and attempts < max_attempts:
        attempts += 1
        print(f"  Run {len(results) + 1}/{runs} ... ", end="", flush=True)
        try:
            timings = run_once(go_bin, package, timeout)
        except subprocess.TimeoutExpired:
            print("TIMEOUT")
            continue
        except Exception as exc:
            print(f"FAILED ({exc})")
            continue

        results.append(timings)
        evaluation_keys = [key for key in timings if "CKKS DAG evaluation" in key]
        if evaluation_keys:
            key = sorted(evaluation_keys)[0]
            print(f"OK ({key}: {timings[key]:.3f} ms)")
        else:
            print(f"OK ({len(timings)} timing items)")

    if len(results) < runs:
        print(f"  Warning: collected {len(results)}/{runs} successful runs")
    return results


def summarize_runs(runs: List[Dict[str, float]]) -> Dict[str, float]:
    grouped: DefaultDict[str, List[float]] = defaultdict(list)
    for run in runs:
        for metric, value in run.items():
            grouped[metric].append(value)
    return {metric: average(values) for metric, values in grouped.items()}


def format_summary(all_summaries: Dict[str, Dict[str, float]], run_counts: Dict[str, int]) -> str:
    lines: List[str] = []
    lines.append("=" * 100)
    lines.append("LATTIGO CKKS DAG 24/48 BENCHMARK AVERAGES")
    lines.append("=" * 100)
    lines.append("")

    for label in all_summaries:
        summary = all_summaries[label]
        lines.append(f"{label}  (successful runs: {run_counts[label]})")
        lines.append("-" * 100)
        for metric, value in sorted(summary.items(), key=lambda item: metric_sort_key(item[0])):
            lines.append(f"{metric:.<78} {value:>14.6f} ms")
        lines.append("")

    lines.append("=" * 100)
    return "\n".join(lines)


def main() -> int:
    args = parse_args()
    if args.runs <= 0 or args.timeout <= 0:
        print("Error: --runs and --timeout must be > 0.", file=sys.stderr)
        return 1

    selected = [
        item for item in PROGRAMS if args.only is None or item[0] in set(args.only)
    ]
    if not selected:
        print("Error: no benchmark programs selected.", file=sys.stderr)
        return 1

    all_summaries: Dict[str, Dict[str, float]] = {}
    run_counts: Dict[str, int] = {}
    for label, package in selected:
        runs = run_program(label, args.go, package, args.runs, args.timeout)
        if not runs:
            print(f"  No successful runs for {label}; skipping summary")
            continue
        all_summaries[label] = summarize_runs(runs)
        run_counts[label] = len(runs)

    if not all_summaries:
        print("Error: no successful benchmark data collected.", file=sys.stderr)
        return 1

    summary_text = format_summary(all_summaries, run_counts)
    print("\n" + summary_text)

    args.output.write_text(summary_text + "\n", encoding="utf-8")
    print(f"\nSummary written to: {args.output.resolve()}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
