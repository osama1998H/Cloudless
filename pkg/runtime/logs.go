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

	task, err := container.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	// Get task IO
	taskIO, err := task.Stdio(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get task stdio: %w", err)
	}

	logCh := make(chan LogEntry, 100)

	go func() {
		defer close(logCh)

		// Read stdout
		go r.streamLogs(ctx, taskIO.Stdout, "stdout", logCh, follow)

		// Read stderr
		go r.streamLogs(ctx, taskIO.Stderr, "stderr", logCh, follow)

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
