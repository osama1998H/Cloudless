#!/bin/bash
# Script to deploy and test the Cloudless HA cluster
# Tests 3-coordinator RAFT cluster with leader election and failover

set -e  # Exit on error

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
COMPOSE_FILE="/Users/osamamuhammed/Cloudless/deployments/docker/docker-compose-ha.yml"
LOG_FILE="/tmp/cloudless-ha-test-logs.txt"
RESULTS_FILE="/tmp/cloudless-ha-test-results.txt"

# Helper functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if docker-compose is available
check_prerequisites() {
    log_info "Checking prerequisites..."

    if ! command -v docker &> /dev/null; then
        log_error "Docker is not installed"
        exit 1
    fi

    if ! command -v docker-compose &> /dev/null; then
        log_error "docker-compose is not installed"
        exit 1
    fi

    log_success "Prerequisites check passed"
}

# Stop existing cluster
stop_existing_cluster() {
    log_info "Stopping any existing cluster..."

    # Stop single-node cluster if running
    docker-compose -f /Users/osamamuhammed/Cloudless/deployments/docker/docker-compose.yml down -v 2>/dev/null || true

    # Stop HA cluster if running
    docker-compose -f "$COMPOSE_FILE" down -v 2>/dev/null || true

    log_success "Existing clusters stopped"
}

# Deploy HA cluster
deploy_ha_cluster() {
    log_info "Deploying HA cluster with 3 coordinators and 3 agents..."

    cd /Users/osamamuhammed/Cloudless/deployments/docker
    docker-compose -f "$COMPOSE_FILE" up -d

    log_success "HA cluster deployed"
}

# Wait for services to be healthy
wait_for_services() {
    log_info "Waiting for services to become healthy (60 seconds)..."
    sleep 60

    log_info "Checking container status..."
    docker-compose -f "$COMPOSE_FILE" ps
}

# Check RAFT cluster formation
check_raft_cluster() {
    log_info "Checking RAFT cluster formation..."

    log_info "Coordinator-1 logs:"
    docker-compose -f "$COMPOSE_FILE" logs coordinator-1 | grep -i "raft\|leader\|bootstrap" | tail -10 || true

    log_info "Coordinator-2 logs:"
    docker-compose -f "$COMPOSE_FILE" logs coordinator-2 | grep -i "raft\|leader\|joined" | tail -10 || true

    log_info "Coordinator-3 logs:"
    docker-compose -f "$COMPOSE_FILE" logs coordinator-3 | grep -i "raft\|leader\|joined" | tail -10 || true
}

# Verify leader election
verify_leader_election() {
    log_info "Verifying leader election..."

    # Check metrics from all coordinators
    log_info "Checking coordinator-1 leader status:"
    curl -s http://localhost:9090/metrics | grep "cloudless_coordinator_is_leader" || log_warning "Could not fetch metrics from coordinator-1"

    log_info "Checking coordinator-2 leader status:"
    curl -s http://localhost:9091/metrics | grep "cloudless_coordinator_is_leader" || log_warning "Could not fetch metrics from coordinator-2"

    log_info "Checking coordinator-3 leader status:"
    curl -s http://localhost:9092/metrics | grep "cloudless_coordinator_is_leader" || log_warning "Could not fetch metrics from coordinator-3"

    # Count leaders (should be exactly 1)
    LEADER_COUNT=0
    if curl -s http://localhost:9090/metrics | grep -q "cloudless_coordinator_is_leader 1"; then
        ((LEADER_COUNT++))
        log_info "Coordinator-1 is leader"
    fi
    if curl -s http://localhost:9091/metrics | grep -q "cloudless_coordinator_is_leader 1"; then
        ((LEADER_COUNT++))
        log_info "Coordinator-2 is leader"
    fi
    if curl -s http://localhost:9092/metrics | grep -q "cloudless_coordinator_is_leader 1"; then
        ((LEADER_COUNT++))
        log_info "Coordinator-3 is leader"
    fi

    if [ "$LEADER_COUNT" -eq 1 ]; then
        log_success "Exactly 1 leader elected (correct!)"
    else
        log_error "Expected 1 leader but found $LEADER_COUNT"
        return 1
    fi
}

# Test leader failover
test_leader_failover() {
    log_info "Testing leader failover..."

    # Identify current leader
    CURRENT_LEADER=""
    if curl -s http://localhost:9090/metrics | grep -q "cloudless_coordinator_is_leader 1"; then
        CURRENT_LEADER="coordinator-1"
    elif curl -s http://localhost:9091/metrics | grep -q "cloudless_coordinator_is_leader 1"; then
        CURRENT_LEADER="coordinator-2"
    elif curl -s http://localhost:9092/metrics | grep -q "cloudless_coordinator_is_leader 1"; then
        CURRENT_LEADER="coordinator-3"
    fi

    if [ -z "$CURRENT_LEADER" ]; then
        log_error "Could not identify current leader"
        return 1
    fi

    log_info "Current leader: $CURRENT_LEADER"

    # Stop the leader
    log_info "Stopping $CURRENT_LEADER..."
    docker-compose -f "$COMPOSE_FILE" stop "$CURRENT_LEADER"

    log_info "Waiting for new leader election (20 seconds)..."
    sleep 20

    # Check for new leader
    log_info "Checking for new leader..."
    NEW_LEADER_COUNT=0

    if [ "$CURRENT_LEADER" != "coordinator-1" ]; then
        if curl -s http://localhost:9090/metrics | grep -q "cloudless_coordinator_is_leader 1"; then
            ((NEW_LEADER_COUNT++))
            log_info "Coordinator-1 became new leader"
        fi
    fi

    if [ "$CURRENT_LEADER" != "coordinator-2" ]; then
        if curl -s http://localhost:9091/metrics | grep -q "cloudless_coordinator_is_leader 1"; then
            ((NEW_LEADER_COUNT++))
            log_info "Coordinator-2 became new leader"
        fi
    fi

    if [ "$CURRENT_LEADER" != "coordinator-3" ]; then
        if curl -s http://localhost:9092/metrics | grep -q "cloudless_coordinator_is_leader 1"; then
            ((NEW_LEADER_COUNT++))
            log_info "Coordinator-3 became new leader"
        fi
    fi

    if [ "$NEW_LEADER_COUNT" -eq 1 ]; then
        log_success "New leader elected successfully!"
    else
        log_error "Leader election failed after stopping $CURRENT_LEADER (found $NEW_LEADER_COUNT leaders)"
        return 1
    fi

    # Restart the stopped coordinator
    log_info "Restarting $CURRENT_LEADER..."
    docker-compose -f "$COMPOSE_FILE" start "$CURRENT_LEADER"

    log_info "Waiting for coordinator to rejoin (20 seconds)..."
    sleep 20

    log_info "Checking if $CURRENT_LEADER rejoined as follower..."
    docker-compose -f "$COMPOSE_FILE" logs "$CURRENT_LEADER" | grep -i "raft\|follower\|joined" | tail -10 || true

    log_success "Leader failover test completed"
}

# Check agent enrollment
check_agent_enrollment() {
    log_info "Checking agent enrollment..."

    log_info "Agent-1 logs:"
    docker-compose -f "$COMPOSE_FILE" logs agent-1 | grep -i "enrolled\|heartbeat\|connected" | tail -5 || true

    log_info "Agent-2 logs:"
    docker-compose -f "$COMPOSE_FILE" logs agent-2 | grep -i "enrolled\|heartbeat\|connected" | tail -5 || true

    log_info "Agent-3 logs:"
    docker-compose -f "$COMPOSE_FILE" logs agent-3 | grep -i "enrolled\|heartbeat\|connected" | tail -5 || true
}

# Collect comprehensive logs
collect_logs() {
    log_info "Collecting comprehensive logs..."

    docker-compose -f "$COMPOSE_FILE" logs > "$LOG_FILE"

    log_success "Logs saved to $LOG_FILE"
}

# Generate test report
generate_report() {
    log_info "Generating test report..."

    {
        echo "======================================"
        echo "Cloudless HA Cluster Test Report"
        echo "======================================"
        echo "Test Date: $(date)"
        echo ""
        echo "=== Container Status ==="
        docker-compose -f "$COMPOSE_FILE" ps
        echo ""
        echo "=== Leader Elections ==="
        grep -c "became leader\|Became leader" "$LOG_FILE" || echo "0"
        echo ""
        echo "=== RAFT Cluster Events ==="
        echo "Nodes joined RAFT cluster:"
        grep -c "joined.*raft\|added to cluster\|Node joined cluster" "$LOG_FILE" || echo "0"
        echo ""
        echo "=== Agent Enrollments ==="
        grep -c "agent enrolled\|node enrolled\|Agent enrolled" "$LOG_FILE" || echo "0"
        echo ""
        echo "=== Heartbeats (sample) ==="
        grep -c "heartbeat\|Heartbeat" "$LOG_FILE" | head -1 || echo "0"
        echo ""
        echo "=== Errors (first 20) ==="
        grep -i "error\|fatal\|panic" "$LOG_FILE" | grep -v "no error\|error=<nil>" | head -20 || echo "No critical errors found"
        echo ""
        echo "======================================"
    } > "$RESULTS_FILE"

    cat "$RESULTS_FILE"

    log_success "Test report saved to $RESULTS_FILE"
}

# Main test flow
main() {
    log_info "Starting Cloudless HA Cluster Test"
    echo ""

    check_prerequisites
    stop_existing_cluster
    deploy_ha_cluster
    wait_for_services

    log_info ""
    log_info "=== Phase 1: RAFT Cluster Formation ==="
    check_raft_cluster

    log_info ""
    log_info "=== Phase 2: Leader Election Verification ==="
    verify_leader_election

    log_info ""
    log_info "=== Phase 3: Leader Failover Test ==="
    test_leader_failover

    log_info ""
    log_info "=== Phase 4: Agent Enrollment Check ==="
    check_agent_enrollment

    log_info ""
    log_info "=== Phase 5: Collecting Logs and Generating Report ==="
    collect_logs
    generate_report

    log_info ""
    log_success "=========================================="
    log_success "HA Cluster Test Completed Successfully!"
    log_success "=========================================="
    log_info ""
    log_info "Next steps:"
    log_info "1. Review logs: $LOG_FILE"
    log_info "2. Review report: $RESULTS_FILE"
    log_info "3. Access Grafana: http://localhost:3001 (admin/admin)"
    log_info "4. Access Prometheus: http://localhost:9093"
    log_info "5. Stop cluster: docker-compose -f $COMPOSE_FILE down -v"
}

# Run main function
main "$@"
