#!/bin/bash
# scripts/benchmark-gc.sh
# Benchmark script to compare Go GC performance (Standard vs GreenteaGC)

set -e

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_PATH="$PROJECT_ROOT/bin/hive"
DATA_DIR="$HOME/hive"
LOGS_DIR="$DATA_DIR/logs"
ITERATIONS=500
CONCURRENCY=12

# Ensure AWS profile is set
export AWS_PROFILE=hive
# Skip SSL verification for local testing
export AWS_CA_BUNDLE="$DATA_DIR/config/server.pem"

# Function to build the binary
build_binary() {
    local experiment="$1"
    echo "🔨 Building with GOEXPERIMENT=$experiment..."
    cd "$PROJECT_ROOT"
    # Clean build to ensure no stale artifacts
    rm -f "$BIN_PATH"
    if [ "$experiment" == "none" ]; then
        go build -ldflags "-s -w" -o "$BIN_PATH" cmd/hive/main.go
    else
        GOEXPERIMENT="$experiment" go build -ldflags "-s -w" -o "$BIN_PATH" cmd/hive/main.go
    fi
}

# Function to start services with GC tracing
start_services_traced() {
    echo "🚀 Starting services with GODEBUG=gctrace=1..."
    export GODEBUG=gctrace=1
    "$PROJECT_ROOT/scripts/start-dev.sh" "$DATA_DIR"
    sleep 10 # More time for services to stabilize
}

# Function to run load test
run_load() {
    echo "🔥 Warming up (20 requests)..."
    seq 20 | xargs -I{} -P $CONCURRENCY aws ec2 describe-instances > /dev/null

    echo "🏃 Running load test ($ITERATIONS requests)..."
    local start_time=$(date +%s%N)

    seq $ITERATIONS | xargs -I{} -P $CONCURRENCY aws ec2 describe-instances > /dev/null

    local end_time=$(date +%s%N)
    local duration=$(( (end_time - start_time) / 1000000 ))
    echo "⏱️  Load test completed in ${duration}ms"
    echo "$duration" > "$LOGS_DIR/last_duration.txt"
}

# Function to analyze GC logs
analyze_logs() {
    local label="$1"
    echo "📊 Analyzing logs for $label..."

    # Analyze the AWS Gateway log (usually the most active)
    local log_file="$LOGS_DIR/awsgw.log"

    if [ ! -f "$log_file" ]; then
        echo "❌ Log file $log_file not found"
        return
    fi

    # Extract GC stats: gc N @T ... total_time ...
    # Format: gc 1 @0.012s 5%: 0.016+0.45+0.003 ms clock, 0.25+0.45/0.70/1.2+0.054 ms cpu, 4->4->0 MB, 5 MB goal, 8 P
    local gc_count=$(grep -c "^gc " "$log_file" || echo 0)
    local total_gc_ms=$(grep "^gc " "$log_file" | awk -F'ms clock' '{print $1}' | awk '{print $NF}' | awk -F'+' '{sum += $1+$2+$3} END {print sum}')

    # Memory usage (max RSS of the process)
    local max_rss=$(ps aux | grep "hive service awsgw start" | grep -v grep | awk '{print $6}' | sort -nr | head -n1)

    local duration=$(cat "$LOGS_DIR/last_duration.txt")

    echo "----------------------------------------"
    echo "Results for $label:"
    echo "Requests:     $ITERATIONS"
    echo "Concurrency:  $CONCURRENCY"
    echo "Total Time:   ${duration}ms"
    echo "Avg Latency:  $(( duration / ITERATIONS ))ms"
    echo "GC Cycles:    $gc_count"
    echo "Total GC Time: ${total_gc_ms}ms"
    echo "Max RSS:      ${max_rss} KB"
    echo "----------------------------------------"

    # Save to a summary file
    echo "$label,$duration,$gc_count,$total_gc_ms,$max_rss" >> "$PROJECT_ROOT/gc_benchmark_summary.csv"
}

# Cleanup
echo "Cleaning up previous runs..."
"$PROJECT_ROOT/scripts/stop-dev.sh" || true
rm -f "$PROJECT_ROOT/gc_benchmark_summary.csv"
echo "Label,TotalTime_ms,GCCycles,TotalGCTime_ms,MaxRSS_KB" > "$PROJECT_ROOT/gc_benchmark_summary.csv"

# 1. Standard GC Benchmark
echo "🔵 PHASE 1: Standard GC"
build_binary "none"
start_services_traced
run_load
analyze_logs "Standard"
"$PROJECT_ROOT/scripts/stop-dev.sh"
sleep 5

# 2. GreenteaGC Benchmark
echo "🟢 PHASE 2: GreenteaGC"
build_binary "greenteagc"
start_services_traced
run_load
analyze_logs "GreenteaGC"
"$PROJECT_ROOT/scripts/stop-dev.sh"

echo "✅ Benchmarking complete! Summary saved to gc_benchmark_summary.csv"
cat "$PROJECT_ROOT/gc_benchmark_summary.csv" | column -t -s,
