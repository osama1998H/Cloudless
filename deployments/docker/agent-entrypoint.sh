#!/bin/sh
set -e

echo "Starting containerd daemon..."

# Start containerd in the background
containerd &
CONTAINERD_PID=$!

# Wait for containerd to be ready
echo "Waiting for containerd to be ready..."
for i in $(seq 1 30); do
    if [ -S /run/containerd/containerd.sock ]; then
        echo "Containerd is ready!"
        break
    fi
    if [ $i -eq 30 ]; then
        echo "ERROR: Containerd failed to start within 30 seconds"
        exit 1
    fi
    sleep 1
done

# Trap SIGTERM and SIGINT to gracefully shutdown
trap 'echo "Shutting down..."; kill -TERM $CONTAINERD_PID $AGENT_PID 2>/dev/null; wait' TERM INT

echo "Starting Cloudless agent..."

# Start the agent with all provided arguments
/usr/local/bin/agent "$@" &
AGENT_PID=$!

# Wait for agent to exit
wait $AGENT_PID
AGENT_EXIT_CODE=$?

# Shutdown containerd
echo "Agent exited with code $AGENT_EXIT_CODE, shutting down containerd..."
kill -TERM $CONTAINERD_PID 2>/dev/null || true
wait $CONTAINERD_PID 2>/dev/null || true

exit $AGENT_EXIT_CODE
