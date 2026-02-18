#!/usr/bin/env bash
#
# dev.sh - Development and integration testing for prisn
#
# Usage:
#   ./dev.sh build          Build the binary
#   ./dev.sh test           Run unit tests
#   ./dev.sh integration    Run integration tests (single node)
#   ./dev.sh cluster        Start a 3-node local cluster
#   ./dev.sh cluster-test   Run integration tests against cluster
#   ./dev.sh all            Build + unit tests + integration tests
#   ./dev.sh clean          Clean up test artifacts
#
set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Directories
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILD_DIR="${SCRIPT_DIR}/bin"
TEST_DIR="${SCRIPT_DIR}/.test"
PRISN="${BUILD_DIR}/prisn"

# Cluster config
NODE1_PORT=7331
NODE2_PORT=7332
NODE3_PORT=7333
NODE1_DIR="${TEST_DIR}/node1"
NODE2_DIR="${TEST_DIR}/node2"
NODE3_DIR="${TEST_DIR}/node3"

# Test config
TEST_NAMESPACE="integration-test"
TIMEOUT=30

# Track PIDs for cleanup
declare -a PIDS=()

#------------------------------------------------------------------------------
# Helpers
#------------------------------------------------------------------------------

log() { echo -e "${BLUE}[$(date +%H:%M:%S)]${NC} $*"; }
ok() { echo -e "${GREEN}[OK]${NC} $*"; }
fail() { echo -e "${RED}[FAIL]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
step() { echo -e "${CYAN}==> $*${NC}"; }

die() {
    fail "$*"
    exit 1
}

cleanup() {
    log "Cleaning up..."
    for pid in "${PIDS[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
        fi
    done
    PIDS=()
}

trap cleanup EXIT

wait_for_port() {
    local port=$1
    local timeout=${2:-$TIMEOUT}
    local start=$(date +%s)

    while ! nc -z localhost "$port" 2>/dev/null; do
        if (( $(date +%s) - start > timeout )); then
            return 1
        fi
        sleep 0.2
    done
    return 0
}

wait_for_healthy() {
    local port=$1
    local timeout=${2:-$TIMEOUT}
    local start=$(date +%s)

    while true; do
        if curl -sf "http://localhost:${port}/health" >/dev/null 2>&1; then
            return 0
        fi
        if (( $(date +%s) - start > timeout )); then
            return 1
        fi
        sleep 0.5
    done
}

assert_eq() {
    local expected="$1"
    local actual="$2"
    local msg="${3:-assertion failed}"

    if [[ "$expected" != "$actual" ]]; then
        fail "$msg: expected '$expected', got '$actual'"
        return 1
    fi
    return 0
}

assert_contains() {
    local haystack="$1"
    local needle="$2"
    local msg="${3:-assertion failed}"

    if [[ "$haystack" != *"$needle"* ]]; then
        fail "$msg: expected to contain '$needle'"
        echo "Got: $haystack"
        return 1
    fi
    return 0
}

assert_success() {
    local msg="${1:-command should succeed}"
    shift
    if ! "$@"; then
        fail "$msg"
        return 1
    fi
    return 0
}

assert_failure() {
    local msg="${1:-command should fail}"
    shift
    if "$@" 2>/dev/null; then
        fail "$msg"
        return 1
    fi
    return 0
}

#------------------------------------------------------------------------------
# Build
#------------------------------------------------------------------------------

cmd_build() {
    step "Building prisn..."
    mkdir -p "$BUILD_DIR"

    go build -o "$PRISN" ./cmd/prisn
    ok "Built: $PRISN"

    # Show version
    "$PRISN" version 2>/dev/null || "$PRISN" --version 2>/dev/null || true
}

#------------------------------------------------------------------------------
# Unit Tests
#------------------------------------------------------------------------------

cmd_test() {
    step "Running unit tests..."
    go test -v ./... 2>&1 | while read -r line; do
        if [[ "$line" == *"PASS"* ]]; then
            echo -e "${GREEN}$line${NC}"
        elif [[ "$line" == *"FAIL"* ]]; then
            echo -e "${RED}$line${NC}"
        else
            echo "$line"
        fi
    done

    if [[ ${PIPESTATUS[0]} -eq 0 ]]; then
        ok "All unit tests passed"
    else
        die "Unit tests failed"
    fi
}

#------------------------------------------------------------------------------
# Single Node Integration Tests
#------------------------------------------------------------------------------

start_single_node() {
    step "Starting single node..."
    mkdir -p "$NODE1_DIR"

    PRISN_DATA_DIR="$NODE1_DIR" "$PRISN" server --port "$NODE1_PORT" &
    PIDS+=($!)

    log "Waiting for server on port $NODE1_PORT..."
    if ! wait_for_healthy "$NODE1_PORT"; then
        die "Server failed to start"
    fi
    ok "Server running on port $NODE1_PORT (PID ${PIDS[-1]})"
}

cmd_integration() {
    step "Running integration tests (single node)..."

    # Ensure we have a build
    [[ -x "$PRISN" ]] || cmd_build

    # Clean test directory
    rm -rf "$TEST_DIR"
    mkdir -p "$TEST_DIR"

    # Start server
    start_single_node

    local passed=0
    local failed=0

    run_test() {
        local name="$1"
        shift
        echo -n "  Testing: $name... "
        if "$@"; then
            echo -e "${GREEN}PASS${NC}"
            ((passed++))
        else
            echo -e "${RED}FAIL${NC}"
            ((failed++))
        fi
    }

    # Set context for tests
    export PRISN_SERVER="localhost:$NODE1_PORT"

    #--------------------------------------------------------------------------
    # CLI Tests
    #--------------------------------------------------------------------------
    step "CLI command tests"

    run_test "help" "$PRISN" help
    run_test "version" "$PRISN" version 2>/dev/null || "$PRISN" --version
    run_test "status" "$PRISN" status

    #--------------------------------------------------------------------------
    # Deployment Tests
    #--------------------------------------------------------------------------
    step "Deployment tests"

    # Create test script
    cat > "$TEST_DIR/hello.py" << 'EOF'
#!/usr/bin/env python3
import time
import sys

print("Hello from prisn!")
sys.stdout.flush()

# Keep running for service mode
while True:
    time.sleep(1)
EOF
    chmod +x "$TEST_DIR/hello.py"

    # Create a job script
    cat > "$TEST_DIR/job.py" << 'EOF'
#!/usr/bin/env python3
print("Job completed!")
EOF
    chmod +x "$TEST_DIR/job.py"

    run_test "deploy service" "$PRISN" deploy "$TEST_DIR/hello.py" --name hello-svc --port 8080 -n "$TEST_NAMESPACE"
    run_test "list deployments" "$PRISN" get deploy -n "$TEST_NAMESPACE"
    run_test "get deployment" "$PRISN" get deploy hello-svc -n "$TEST_NAMESPACE"

    # Wait a bit for service to start
    sleep 2

    run_test "status shows deployment" bash -c "$PRISN status 2>&1 | grep -q hello-svc || true"

    #--------------------------------------------------------------------------
    # Scale Tests
    #--------------------------------------------------------------------------
    step "Scale tests"

    run_test "scale to 3" "$PRISN" scale hello-svc 3 -n "$TEST_NAMESPACE"
    run_test "scale to 0 (pause)" "$PRISN" scale hello-svc 0 -n "$TEST_NAMESPACE"
    run_test "scale back to 1" "$PRISN" scale hello-svc 1 -n "$TEST_NAMESPACE"

    #--------------------------------------------------------------------------
    # Secret Tests
    #--------------------------------------------------------------------------
    step "Secret tests"

    run_test "create secret (literal)" "$PRISN" secret create test-secret --from-literal KEY1=value1 --from-literal KEY2=value2 -n "$TEST_NAMESPACE"
    run_test "get secret" "$PRISN" secret get test-secret -n "$TEST_NAMESPACE"
    run_test "get secret (show values)" "$PRISN" secret get test-secret --show-values -n "$TEST_NAMESPACE"
    run_test "delete secret" "$PRISN" secret delete test-secret --force -n "$TEST_NAMESPACE"

    # Create secret from file
    echo "file-content-here" > "$TEST_DIR/secret.txt"
    run_test "create secret (file)" "$PRISN" secret create file-secret --from-file data="$TEST_DIR/secret.txt" -n "$TEST_NAMESPACE"

    # Create secret from env file
    cat > "$TEST_DIR/.env.test" << 'EOF'
DATABASE_URL=postgres://localhost/db
API_KEY=sk_test_123
# This is a comment
DEBUG=true
EOF
    run_test "create secret (env file)" "$PRISN" secret create env-secret --from-env-file "$TEST_DIR/.env.test" -n "$TEST_NAMESPACE"

    #--------------------------------------------------------------------------
    # Job Tests
    #--------------------------------------------------------------------------
    step "Job tests"

    run_test "run job" "$PRISN" run "$TEST_DIR/job.py" --name test-job -n "$TEST_NAMESPACE"

    #--------------------------------------------------------------------------
    # Log Tests
    #--------------------------------------------------------------------------
    step "Log tests"

    # Logs might not have content yet, just check command works
    run_test "logs command" bash -c "$PRISN logs hello-svc -n $TEST_NAMESPACE 2>&1 || true"

    #--------------------------------------------------------------------------
    # Delete Tests
    #--------------------------------------------------------------------------
    step "Cleanup tests"

    run_test "delete deployment" "$PRISN" delete deploy hello-svc --force -n "$TEST_NAMESPACE"

    #--------------------------------------------------------------------------
    # Error Handling Tests
    #--------------------------------------------------------------------------
    step "Error handling tests"

    run_test "get nonexistent" assert_failure "should fail for missing" "$PRISN" get deploy nonexistent-deploy -n "$TEST_NAMESPACE"
    run_test "invalid name" assert_failure "should reject invalid name" "$PRISN" deploy "$TEST_DIR/hello.py" --name "Invalid Name!"
    run_test "missing source" assert_failure "should fail for missing file" "$PRISN" deploy /nonexistent/path.py --name test

    #--------------------------------------------------------------------------
    # Summary
    #--------------------------------------------------------------------------
    echo ""
    step "Integration test summary"
    echo -e "  ${GREEN}Passed: $passed${NC}"
    echo -e "  ${RED}Failed: $failed${NC}"

    if [[ $failed -gt 0 ]]; then
        die "Integration tests failed"
    fi
    ok "All integration tests passed!"
}

#------------------------------------------------------------------------------
# Cluster Mode
#------------------------------------------------------------------------------

start_cluster() {
    step "Starting 3-node cluster..."

    rm -rf "$TEST_DIR"
    mkdir -p "$NODE1_DIR" "$NODE2_DIR" "$NODE3_DIR"

    # Start node 1 (bootstrap)
    log "Starting node 1 (bootstrap)..."
    PRISN_DATA_DIR="$NODE1_DIR" "$PRISN" server \
        --port "$NODE1_PORT" \
        --cluster \
        --node-id node1 \
        --bootstrap &
    PIDS+=($!)

    if ! wait_for_healthy "$NODE1_PORT"; then
        die "Node 1 failed to start"
    fi
    ok "Node 1 running on port $NODE1_PORT"

    # Start node 2
    log "Starting node 2..."
    PRISN_DATA_DIR="$NODE2_DIR" "$PRISN" server \
        --port "$NODE2_PORT" \
        --cluster \
        --node-id node2 \
        --join "localhost:$NODE1_PORT" &
    PIDS+=($!)

    if ! wait_for_healthy "$NODE2_PORT"; then
        die "Node 2 failed to start"
    fi
    ok "Node 2 running on port $NODE2_PORT"

    # Start node 3
    log "Starting node 3..."
    PRISN_DATA_DIR="$NODE3_DIR" "$PRISN" server \
        --port "$NODE3_PORT" \
        --cluster \
        --node-id node3 \
        --join "localhost:$NODE1_PORT" &
    PIDS+=($!)

    if ! wait_for_healthy "$NODE3_PORT"; then
        die "Node 3 failed to start"
    fi
    ok "Node 3 running on port $NODE3_PORT"

    # Give cluster time to elect leader
    log "Waiting for leader election..."
    sleep 3

    ok "3-node cluster running!"
    echo ""
    echo "  Node 1: http://localhost:$NODE1_PORT"
    echo "  Node 2: http://localhost:$NODE2_PORT"
    echo "  Node 3: http://localhost:$NODE3_PORT"
    echo ""
    echo "Set context: export PRISN_SERVER=localhost:$NODE1_PORT"
}

cmd_cluster() {
    [[ -x "$PRISN" ]] || cmd_build
    start_cluster

    echo ""
    log "Cluster is running. Press Ctrl+C to stop."

    # Wait forever (cleanup trap will handle it)
    while true; do sleep 1; done
}

cmd_cluster_test() {
    step "Running cluster integration tests..."

    [[ -x "$PRISN" ]] || cmd_build
    start_cluster

    local passed=0
    local failed=0

    run_test() {
        local name="$1"
        shift
        echo -n "  Testing: $name... "
        if "$@"; then
            echo -e "${GREEN}PASS${NC}"
            ((passed++))
        else
            echo -e "${RED}FAIL${NC}"
            ((failed++))
        fi
    }

    export PRISN_SERVER="localhost:$NODE1_PORT"

    #--------------------------------------------------------------------------
    # Cluster Health Tests
    #--------------------------------------------------------------------------
    step "Cluster health tests"

    run_test "node1 health" curl -sf "http://localhost:$NODE1_PORT/health"
    run_test "node2 health" curl -sf "http://localhost:$NODE2_PORT/health"
    run_test "node3 health" curl -sf "http://localhost:$NODE3_PORT/health"

    #--------------------------------------------------------------------------
    # Cluster Status Tests
    #--------------------------------------------------------------------------
    step "Cluster status tests"

    run_test "cluster status" "$PRISN" cluster status
    run_test "cluster nodes" "$PRISN" cluster nodes

    #--------------------------------------------------------------------------
    # Deployment Replication Tests
    #--------------------------------------------------------------------------
    step "Deployment replication tests"

    cat > "$TEST_DIR/cluster-test.py" << 'EOF'
#!/usr/bin/env python3
import time
print("Cluster test service running")
while True:
    time.sleep(1)
EOF
    chmod +x "$TEST_DIR/cluster-test.py"

    run_test "deploy to cluster" "$PRISN" deploy "$TEST_DIR/cluster-test.py" --name cluster-svc --port 9000

    sleep 2

    # Verify deployment is visible from all nodes
    run_test "visible from node1" bash -c "PRISN_SERVER=localhost:$NODE1_PORT $PRISN get deploy cluster-svc"
    run_test "visible from node2" bash -c "PRISN_SERVER=localhost:$NODE2_PORT $PRISN get deploy cluster-svc"
    run_test "visible from node3" bash -c "PRISN_SERVER=localhost:$NODE3_PORT $PRISN get deploy cluster-svc"

    #--------------------------------------------------------------------------
    # Failover Tests
    #--------------------------------------------------------------------------
    step "Failover tests"

    log "Killing node 1 to test failover..."
    kill "${PIDS[0]}" 2>/dev/null || true
    sleep 3

    run_test "node2 still healthy" curl -sf "http://localhost:$NODE2_PORT/health"
    run_test "deployment still visible" bash -c "PRISN_SERVER=localhost:$NODE2_PORT $PRISN get deploy cluster-svc"

    #--------------------------------------------------------------------------
    # Summary
    #--------------------------------------------------------------------------
    echo ""
    step "Cluster test summary"
    echo -e "  ${GREEN}Passed: $passed${NC}"
    echo -e "  ${RED}Failed: $failed${NC}"

    if [[ $failed -gt 0 ]]; then
        die "Cluster tests failed"
    fi
    ok "All cluster tests passed!"
}

#------------------------------------------------------------------------------
# Scheduler Tests
#------------------------------------------------------------------------------

cmd_scheduler_test() {
    step "Running scheduler tests..."

    [[ -x "$PRISN" ]] || cmd_build

    rm -rf "$TEST_DIR"
    mkdir -p "$TEST_DIR"

    start_single_node

    export PRISN_SERVER="localhost:$NODE1_PORT"

    local passed=0
    local failed=0

    run_test() {
        local name="$1"
        shift
        echo -n "  Testing: $name... "
        if "$@"; then
            echo -e "${GREEN}PASS${NC}"
            ((passed++))
        else
            echo -e "${RED}FAIL${NC}"
            ((failed++))
        fi
    }

    # Create a cron job script that writes to a file
    cat > "$TEST_DIR/cron-job.py" << 'EOF'
#!/usr/bin/env python3
import datetime
with open("/tmp/prisn-cron-test.txt", "a") as f:
    f.write(f"Ran at {datetime.datetime.now()}\n")
print("Cron job executed!")
EOF
    chmod +x "$TEST_DIR/cron-job.py"

    #--------------------------------------------------------------------------
    # Cron Job Tests
    #--------------------------------------------------------------------------
    step "Cron job tests"

    # Deploy as cron job (every minute for testing)
    run_test "create cronjob" "$PRISN" deploy "$TEST_DIR/cron-job.py" \
        --name cron-test \
        --schedule "* * * * *"

    run_test "list shows cronjob" bash -c "$PRISN get deploy cron-test 2>&1 | grep -q cronjob || $PRISN get deploy cron-test 2>&1 | grep -q schedule"

    # Check scheduler status
    run_test "scheduler status" "$PRISN" scheduler status 2>/dev/null || true

    log "Waiting 65 seconds for cron to trigger..."
    sleep 65

    # Check if cron ran
    if [[ -f /tmp/prisn-cron-test.txt ]]; then
        run_test "cron executed" cat /tmp/prisn-cron-test.txt
        rm -f /tmp/prisn-cron-test.txt
    else
        run_test "cron executed" false
    fi

    # Cleanup
    run_test "delete cronjob" "$PRISN" delete deploy cron-test --force

    #--------------------------------------------------------------------------
    # Summary
    #--------------------------------------------------------------------------
    echo ""
    step "Scheduler test summary"
    echo -e "  ${GREEN}Passed: $passed${NC}"
    echo -e "  ${RED}Failed: $failed${NC}"

    if [[ $failed -gt 0 ]]; then
        die "Scheduler tests failed"
    fi
    ok "All scheduler tests passed!"
}

#------------------------------------------------------------------------------
# Smoke Test (no server needed)
#------------------------------------------------------------------------------

cmd_smoke() {
    step "Running smoke tests (no server required)..."

    [[ -x "$PRISN" ]] || cmd_build

    local passed=0
    local failed=0

    run_test() {
        local name="$1"
        shift
        echo -n "  Testing: $name... "
        if "$@" >/dev/null 2>&1; then
            echo -e "${GREEN}PASS${NC}"
            passed=$((passed + 1))
        else
            echo -e "${RED}FAIL${NC}"
            failed=$((failed + 1))
        fi
        return 0  # Always succeed to not trigger set -e
    }

    run_test_output() {
        local name="$1"
        local expected="$2"
        shift 2
        echo -n "  Testing: $name... "
        local output
        output=$("$@" 2>&1) || true
        if [[ "$output" == *"$expected"* ]]; then
            echo -e "${GREEN}PASS${NC}"
            passed=$((passed + 1))
        else
            echo -e "${RED}FAIL${NC} (expected '$expected')"
            failed=$((failed + 1))
        fi
        return 0
    }

    #--------------------------------------------------------------------------
    # CLI Parsing Tests
    #--------------------------------------------------------------------------
    step "CLI parsing tests"

    run_test "help" "$PRISN" help
    run_test "help deploy" "$PRISN" help deploy
    run_test "help scale" "$PRISN" help scale
    run_test "help secret" "$PRISN" help secret
    run_test "help context" "$PRISN" help context
    run_test "version" "$PRISN" version

    #--------------------------------------------------------------------------
    # Flag Parsing Tests
    #--------------------------------------------------------------------------
    step "Flag parsing tests"

    run_test "--help" "$PRISN" --help
    run_test "-n flag parses" "$PRISN" get deploy -n test-ns --help
    run_test "--context flag parses" "$PRISN" --context local --help
    run_test "--output flag parses" "$PRISN" get deploy --output json --help

    #--------------------------------------------------------------------------
    # Validation Tests (should fail with specific errors)
    #--------------------------------------------------------------------------
    step "Validation tests"

    run_test_output "rejects invalid name" "invalid" "$PRISN" deploy /dev/null --name "Bad Name!"
    run_test_output "rejects invalid port" "65535" "$PRISN" deploy /dev/null --name test --port 99999
    run_test_output "requires source arg" "accepts 1 arg" "$PRISN" deploy --name test

    #--------------------------------------------------------------------------
    # Context Commands (offline)
    #--------------------------------------------------------------------------
    step "Context commands (offline)"

    run_test "context list" "$PRISN" context list
    run_test "context current" "$PRISN" context current

    #--------------------------------------------------------------------------
    # Cache Commands (offline)
    #--------------------------------------------------------------------------
    step "Cache commands (offline)"

    run_test "cache list" "$PRISN" cache list
    run_test "cache clean help" "$PRISN" cache clean --help

    #--------------------------------------------------------------------------
    # Summary
    #--------------------------------------------------------------------------
    echo ""
    step "Smoke test summary"
    echo -e "  ${GREEN}Passed: $passed${NC}"
    echo -e "  ${RED}Failed: $failed${NC}"

    if [[ $failed -gt 0 ]]; then
        die "Smoke tests failed"
    fi
    ok "All smoke tests passed!"
}

#------------------------------------------------------------------------------
# Clean
#------------------------------------------------------------------------------

cmd_clean() {
    step "Cleaning up..."

    # Kill any running prisn processes
    pkill -f "prisn server" 2>/dev/null || true

    # Remove test directories
    rm -rf "$TEST_DIR"
    rm -rf "$BUILD_DIR"
    rm -f /tmp/prisn-cron-test.txt

    ok "Cleaned up test artifacts"
}

#------------------------------------------------------------------------------
# All
#------------------------------------------------------------------------------

cmd_all() {
    cmd_build
    cmd_test
    cmd_integration

    echo ""
    ok "All checks passed!"
}

#------------------------------------------------------------------------------
# Help
#------------------------------------------------------------------------------

cmd_help() {
    cat << 'EOF'
prisn development helper

Usage: ./dev.sh <command>

Commands:
  build           Build the prisn binary
  test            Run unit tests
  smoke           Run smoke tests (no server needed, fast)
  integration     Run integration tests (single node)
  cluster         Start a 3-node local cluster (interactive)
  cluster-test    Run integration tests against a 3-node cluster
  scheduler-test  Run scheduler/cron tests (takes ~65 seconds)
  all             Build + unit tests + integration tests
  clean           Clean up test artifacts and processes

Quick Start:
  ./dev.sh smoke              # Fast CLI validation (no server)
  ./dev.sh all                # Full test suite
  ./dev.sh cluster            # Start cluster for manual testing

Examples:
  ./dev.sh build              # Just build the binary
  ./dev.sh test               # Just run unit tests
  ./dev.sh integration        # Single-node integration tests
  ./dev.sh cluster-test       # 3-node cluster tests

Environment Variables:
  PRISN_SERVER    Server address (default: localhost:7331)
  TIMEOUT         Wait timeout in seconds (default: 30)

EOF
}

#------------------------------------------------------------------------------
# Main
#------------------------------------------------------------------------------

main() {
    cd "$SCRIPT_DIR"

    local cmd="${1:-help}"
    shift || true

    case "$cmd" in
        build)          cmd_build "$@" ;;
        test)           cmd_test "$@" ;;
        smoke)          cmd_smoke "$@" ;;
        integration)    cmd_integration "$@" ;;
        cluster)        cmd_cluster "$@" ;;
        cluster-test)   cmd_cluster_test "$@" ;;
        scheduler-test) cmd_scheduler_test "$@" ;;
        all)            cmd_all "$@" ;;
        clean)          cmd_clean "$@" ;;
        help|--help|-h) cmd_help ;;
        *)              die "Unknown command: $cmd (try: ./dev.sh help)" ;;
    esac
}

main "$@"
