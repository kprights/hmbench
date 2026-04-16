#!/usr/bin/env python3
"""
Benchmark runner for the Lattigo CKKS DAG examples.
Runs each program multiple times and aggregates timing statistics.
"""

import argparse
import re
import statistics
import subprocess
import sys
from collections import Counter, defaultdict
from pathlib import Path


PROGRAMS = {
    "single": "./cmd/ckks_dag_single_thread/main.go",
    "manual": "./cmd/ckks_dag_manual_parallel/main.go",
}

TOP_LEVEL_PREFIXES = (
    "Single-thread ",
    "Manual-parallel ",
    "OMP-hierarchical ",
    "Parallel ",
)

GROUP_NAMES = ("branch_add", "branch_quad", "branch_cross", "merge_tail", "fanout")

OP_LINE_RE = re.compile(
    r"^\s*\[([A-Za-z0-9_.-]+)\]\s+([A-Za-z0-9_./-]+)\s+TIME:\s+([\d.]+)\s*(ms|s)\b"
)
TIME_LINE_RE = re.compile(r"^\s*(.+?)\s+TIME:\s+([\d.]+)\s*(ms|s)\b")


class BenchmarkCollector:
    """Collects timing data for each program across multiple runs."""

    def __init__(self):
        self.data = defaultdict(lambda: defaultdict(list))

    def add_run(self, program_name, timing_data):
        for key, value in timing_data.items():
            self.data[program_name][key].append(value)

    def get_averages(self, program_name):
        averages = {}
        for key, values in self.data.get(program_name, {}).items():
            if values:
                averages[key] = statistics.mean(values)
        return averages

    def get_all_programs(self):
        return list(self.data.keys())


def parse_time_to_ms(value_str, unit):
    value = float(value_str)
    return value if unit == "ms" else value * 1000.0


def normalize_top_level_label(label):
    label = " ".join(label.split())
    for prefix in TOP_LEVEL_PREFIXES:
        if label.startswith(prefix):
            return label[len(prefix):]
    return label


def normalize_trace_entries(entries):
    counts = Counter(entries)
    seen = Counter()
    normalized = []

    for entry in entries:
        seen[entry] += 1
        if counts[entry] > 1:
            normalized.append(f"{entry}#{seen[entry]}")
        else:
            normalized.append(entry)

    return normalized


def parse_timing_output(output):
    """
    Parse timing data from program output.

    Supported formats:
      - "Single-thread CKKS setup TIME: 53.99ms"
      - "  branch_add TIME: 1294.916 ms"
      - "  [branch_add] rotate_1 TIME: 425.783 ms | ..."
    """
    raw_entries = []
    values = []

    for line in output.splitlines():
        op_match = OP_LINE_RE.match(line)
        if op_match:
            group, op, value_str, unit = op_match.groups()
            raw_entries.append(f"{group}.{op}")
            values.append(parse_time_to_ms(value_str, unit))
            continue

        time_match = TIME_LINE_RE.match(line)
        if not time_match:
            continue

        label, value_str, unit = time_match.groups()
        label = normalize_top_level_label(label)
        raw_entries.append(label)
        values.append(parse_time_to_ms(value_str, unit))

    normalized_entries = normalize_trace_entries(raw_entries)
    timings = {}
    for key, value in zip(normalized_entries, values):
        timings[key] = value

    return timings


def run_benchmark(program_path, num_runs):
    """
    Run one Go benchmark program multiple times.

    Returns:
        Tuple[str, list[dict[str, float]]]
    """
    program_name = Path(program_path).parent.name
    results = []

    print(f"\n{'=' * 70}")
    print(f"Running {program_name} ({num_runs} iterations)...")
    print(f"{'=' * 70}")

    for run in range(1, num_runs + 1):
        try:
            result = subprocess.run(
                ["go", "run", program_path],
                cwd="/home/guoshuai/github/hmbench/lattigo",
                capture_output=True,
                text=True,
                timeout=300,
            )
        except subprocess.TimeoutExpired:
            print(f"  Run {run}/{num_runs}: TIMEOUT")
            continue
        except Exception as exc:
            print(f"  Run {run}/{num_runs}: ERROR ({exc})")
            continue

        if result.returncode != 0:
            print(f"  Run {run}/{num_runs}: FAILED (exit code {result.returncode})")
            if result.stderr.strip():
                print(f"    stderr: {result.stderr.strip().splitlines()[-1]}")
            continue

        timings = parse_timing_output(result.stdout + result.stderr)
        if timings:
            results.append(timings)
            print(f"  Run {run}/{num_runs}: OK ({len(timings)} timing points)")
        else:
            print(f"  Run {run}/{num_runs}: FAILED (no timing data found)")

    print(f"\nCompleted {len(results)}/{num_runs} successful runs")
    return program_name, results


def is_group_key(key):
    return key in GROUP_NAMES


def is_operation_key(key):
    return "." in key


def format_section(title, items):
    if not items:
        return

    print(f"\n  {title}:")
    for key, value in items:
        print(f"    {key:.<50} {value:>10.2f} ms")


def print_results(collector):
    print(f"\n\n{'=' * 70}")
    print("BENCHMARK RESULTS - AVERAGE TIMINGS")
    print(f"{'=' * 70}\n")

    for program_name in collector.get_all_programs():
        averages = collector.get_averages(program_name)
        print(f"\n{program_name}")
        print("-" * 70)

        if not averages:
            print("  No data collected")
            continue

        timing_items = sorted(averages.items())
        used_keys = set()

        setup_keys = [
            item
            for item in timing_items
            if not is_operation_key(item[0])
            and (
                "setup" in item[0].lower()
                or "generation" in item[0].lower()
                or "runtime object" in item[0].lower()
            )
        ]
        used_keys.update(key for key, _ in setup_keys)
        format_section("Setup & Key Generation", setup_keys)

        encode_keys = [
            item
            for item in timing_items
            if not is_operation_key(item[0]) and "encode" in item[0].lower()
        ]
        used_keys.update(key for key, _ in encode_keys)
        format_section("Encoding", encode_keys)

        encrypt_keys = [
            item
            for item in timing_items
            if not is_operation_key(item[0]) and "encrypt" in item[0].lower()
        ]
        used_keys.update(key for key, _ in encrypt_keys)
        format_section("Encryption", encrypt_keys)

        eval_keys = [
            item
            for item in timing_items
            if not is_operation_key(item[0]) and "evaluation" in item[0].lower()
        ]
        used_keys.update(key for key, _ in eval_keys)
        format_section("Evaluation", eval_keys)

        group_keys = [item for item in timing_items if is_group_key(item[0])]
        used_keys.update(key for key, _ in group_keys)
        format_section("Operation Group Timings", group_keys)

        operation_keys = [item for item in timing_items if is_operation_key(item[0])]
        used_keys.update(key for key, _ in operation_keys)
        format_section("Detailed Operations", operation_keys)

        other_keys = [item for item in timing_items if item[0] not in used_keys]
        format_section("Other Timings", other_keys)


def parse_args():
    parser = argparse.ArgumentParser(description="Run and summarize CKKS DAG benchmarks.")
    parser.add_argument(
        "--runs",
        type=int,
        default=30,
        help="Number of runs per program (default: 30)",
    )
    parser.add_argument(
        "--program",
        choices=("all", "single", "manual"),
        default="all",
        help="Which benchmark program to run (default: all)",
    )
    return parser.parse_args()


def main():
    args = parse_args()
    if args.runs < 1:
        print("Error: --runs must be at least 1", file=sys.stderr)
        sys.exit(1)

    selected = PROGRAMS.items() if args.program == "all" else [(args.program, PROGRAMS[args.program])]

    print("\nBenchmark Configuration:")
    print("  Build mode: go run")
    print("  Working directory: /home/guoshuai/github/hmbench/lattigo")
    print(f"  Number of runs per program: {args.runs}")
    print(f"  Total programs: {len(list(selected))}")

    collector = BenchmarkCollector()

    for _, program_path in selected:
        program_name, results = run_benchmark(program_path, args.runs)
        for timing_data in results:
            collector.add_run(program_name, timing_data)

    print_results(collector)
    print(f"\n{'=' * 70}\n")


if __name__ == "__main__":
    main()
