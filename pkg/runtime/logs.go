package runtime

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"time"

	"go.uber.org/zap"
)

// GetContainerLogs retrieves logs from a container
func (r *ContainerdRuntime) GetContainerLogs(ctx context.Context, containerID string, follow bool) (<-chan LogEntry, error) {
	ctx = r.withNamespace(ctx)

	container, err := r.client.LoadContainer(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to load container: %w", err)
	}

	_, err = container.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	// TODO(osama): Implement proper container log streaming. See issue #15.
	// In newer containerd versions, stdio must be managed through cio.Creator at task creation.
	// For MVP, we return an empty channel - full implementation requires:
	// 1. Configure cio.Creator with log file paths at task.Create()
	// 2. Stream from containerd log files
	// 3. Support log rotation and retention
	logCh := make(chan LogEntry, 100)

	go func() {
		defer close(logCh)
		r.logger.Warn("Container log streaming not yet implemented",
			zap.String("container_id", containerID))
		<-ctx.Done()
	}()

	return logCh, nil
}

// streamLogs streams logs from a reader to a channel
func (r *ContainerdRuntime) streamLogs(ctx context.Context, reader io.Reader, stream string, logCh chan<- LogEntry, follow bool) {
	scanner := bufio.NewScanner(reader)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			if !scanner.Scan() {
				if scanner.Err() != nil {
					r.logger.Warn("Error reading logs",
						zap.String("stream", stream),
						zap.Error(scanner.Err()),
					)
				}
				if !follow {
					return
				}
				// If following, wait a bit and continue
				time.Sleep(100 * time.Millisecond)
				continue
			}

			entry := LogEntry{
				Timestamp: time.Now(),
				Stream:    stream,
				Log:       scanner.Text(),
			}

			select {
			case logCh <- entry:
			case <-ctx.Done():
				return
			}
		}
	}
}

// GetContainerLogsTail retrieves the last N lines of logs
func (r *ContainerdRuntime) GetContainerLogsTail(ctx context.Context, containerID string, lines int) ([]LogEntry, error) {
	logCh, err := r.GetContainerLogs(ctx, containerID, false)
	if err != nil {
		return nil, err
	}

	// Collect logs
	var logs []LogEntry
	for entry := range logCh {
		logs = append(logs, entry)
		if len(logs) > lines*2 { // Buffer more than needed
			logs = logs[1:] // Remove oldest
		}
	}

	// Return last N lines
	if len(logs) > lines {
		logs = logs[len(logs)-lines:]
	}

	return logs, nil
}
