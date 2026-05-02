#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_DIR="$ROOT_DIR/output"
TIMESTAMP="$(date '+%Y%m%d_%H%M%S')"
SUMMARY_FILE="$OUTPUT_DIR/lattigo_ckks_dag_24_48_benchmark_summary_${TIMESTAMP}.txt"
LOG_FILE="$OUTPUT_DIR/lattigo_ckks_dag_24_48_run_${TIMESTAMP}.log"

mkdir -p "$OUTPUT_DIR"

cd "$ROOT_DIR"
python3 benchmark_runner.py --output "$SUMMARY_FILE" "$@" | tee "$LOG_FILE"
