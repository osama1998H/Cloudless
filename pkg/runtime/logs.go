package runtime

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
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

	// Verify container has a task (is running or has run)
	_, err = container.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	// Get log path from container labels
	labels, err := container.Labels(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container labels: %w", err)
	}

	logPath, ok := labels["cloudless.log_path"]
	if !ok || logPath == "" {
		// Fallback to default log path
		logPath = fmt.Sprintf("/var/log/cloudless/containers/%s.log", containerID)
		r.logger.Warn("Log path not found in labels, using default",
			zap.String("container_id", containerID),
			zap.String("log_path", logPath))
	}

	// Open log file
	file, err := os.Open(logPath)
	if err != nil {
		// Log file not found - container may not have produced logs yet
		r.logger.Warn("Log file not found, returning empty stream",
			zap.String("container_id", containerID),
			zap.String("log_path", logPath),
			zap.Error(err))

		logCh := make(chan LogEntry, 100)
		go func() {
			defer close(logCh)
			<-ctx.Done()
		}()
		return logCh, nil
	}

	// Create buffered channel for log entries
	logCh := make(chan LogEntry, 100)

	// Start goroutine to stream logs
	go func() {
		defer close(logCh)
		defer file.Close()

		r.streamLogs(ctx, file, "stdout", logCh, follow)
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
				Line:      scanner.Text(),
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
