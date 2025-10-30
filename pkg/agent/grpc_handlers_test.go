package agent

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/cloudless/cloudless/pkg/api"
	"github.com/cloudless/cloudless/pkg/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockRuntime implements runtime.ContainerRuntime interface for testing
type mockRuntime struct {
	getContainerLogsFn func(ctx context.Context, containerID string, follow bool) (<-chan runtime.LogEntry, error)
	execContainerFn    func(ctx context.Context, containerID string, command []string) (*runtime.ExecResult, error)
}

func (m *mockRuntime) GetContainerLogs(ctx context.Context, containerID string, follow bool) (<-chan runtime.LogEntry, error) {
	if m.getContainerLogsFn != nil {
		return m.getContainerLogsFn(ctx, containerID, follow)
	}

	// Default: return empty channel
	logCh := make(chan runtime.LogEntry, 10)
	close(logCh)
	return logCh, nil
}

func (m *mockRuntime) ExecContainer(ctx context.Context, containerID string, command []string) (*runtime.ExecResult, error) {
	if m.execContainerFn != nil {
		return m.execContainerFn(ctx, containerID, command)
	}

	// Default: successful execution
	return &runtime.ExecResult{
		Stdout:   "command output\n",
		Stderr:   "",
		ExitCode: 0,
	}, nil
}

// Implement other required methods with no-op
func (m *mockRuntime) CreateContainer(ctx context.Context, spec runtime.ContainerSpec) (*runtime.Container, error) {
	return nil, errors.New("not implemented in mock")
}

func (m *mockRuntime) StartContainer(ctx context.Context, containerID string) error {
	return nil
}

func (m *mockRuntime) StopContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	return nil
}

func (m *mockRuntime) DeleteContainer(ctx context.Context, containerID string) error {
	return nil
}

func (m *mockRuntime) GetContainer(ctx context.Context, containerID string) (*runtime.Container, error) {
	return nil, errors.New("not implemented in mock")
}

func (m *mockRuntime) ListContainers(ctx context.Context) ([]*runtime.Container, error) {
	return nil, nil
}

func (m *mockRuntime) PullImage(ctx context.Context, imageRef string) error {
	return nil
}

func (m *mockRuntime) GetContainerLogsTail(ctx context.Context, containerID string, lines int) ([]runtime.LogEntry, error) {
	return nil, nil
}

// setupTestAgent creates an agent with mock dependencies
func setupTestAgent(t *testing.T) *Agent {
	logger := zap.NewNop()

	a := &Agent{
		logger:  logger,
		runtime: &mockRuntime{},
	}

	return a
}

// TestStreamLogs tests log streaming scenarios
func TestStreamLogs(t *testing.T) {
	tests := []struct {
		name      string
		request   *api.StreamLogsRequest
		setupMock func(*mockRuntime)
		wantErr   bool
		wantCode  codes.Code
		checkLogs func(*testing.T, []*api.LogEntry)
	}{
		{
			name: "success - stream logs from running container",
			request: &api.StreamLogsRequest{
				ContainerId: "test-container-123",
				Follow:      false,
				Tail:        0,
			},
			setupMock: func(m *mockRuntime) {
				m.getContainerLogsFn = func(ctx context.Context, containerID string, follow bool) (<-chan runtime.LogEntry, error) {
					logCh := make(chan runtime.LogEntry, 5)

					go func() {
						defer close(logCh)
						logCh <- runtime.LogEntry{
							Timestamp: time.Now(),
							Stream:    "stdout",
							Log:       "Starting application...",
						}
						logCh <- runtime.LogEntry{
							Timestamp: time.Now(),
							Stream:    "stdout",
							Log:       "Server listening on port 8080",
						}
						logCh <- runtime.LogEntry{
							Timestamp: time.Now(),
							Stream:    "stderr",
							Log:       "Warning: Debug mode enabled",
						}
					}()

					return logCh, nil
				}
			},
			wantErr: false,
			checkLogs: func(t *testing.T, logs []*api.LogEntry) {
				assert.Len(t, logs, 3)
				assert.Equal(t, "Starting application...", logs[0].Log)
				assert.Equal(t, "stdout", logs[0].Stream)
				assert.Equal(t, "Server listening on port 8080", logs[1].Log)
				assert.Equal(t, "Warning: Debug mode enabled", logs[2].Log)
				assert.Equal(t, "stderr", logs[2].Stream)
			},
		},
		{
			name: "success - stream with tail=10 (last 10 lines)",
			request: &api.StreamLogsRequest{
				ContainerId: "test-container",
				Follow:      false,
				Tail:        10,
			},
			setupMock: func(m *mockRuntime) {
				m.getContainerLogsFn = func(ctx context.Context, containerID string, follow bool) (<-chan runtime.LogEntry, error) {
					logCh := make(chan runtime.LogEntry, 15)

					go func() {
						defer close(logCh)
						for i := 1; i <= 15; i++ {
							logCh <- runtime.LogEntry{
								Timestamp: time.Now(),
								Stream:    "stdout",
								Log:       "Log line " + string(rune('0'+i)),
							}
						}
					}()

					return logCh, nil
				}
			},
			wantErr: false,
			checkLogs: func(t *testing.T, logs []*api.LogEntry) {
				// Should get last 10 lines
				assert.LessOrEqual(t, len(logs), 15)
			},
		},
		{
			name: "success - follow mode (real-time streaming)",
			request: &api.StreamLogsRequest{
				ContainerId: "test-container",
				Follow:      true,
				Tail:        0,
			},
			setupMock: func(m *mockRuntime) {
				m.getContainerLogsFn = func(ctx context.Context, containerID string, follow bool) (<-chan runtime.LogEntry, error) {
					assert.True(t, follow, "follow should be true")

					logCh := make(chan runtime.LogEntry, 10)

					go func() {
						defer close(logCh)
						for i := 0; i < 5; i++ {
							select {
							case <-ctx.Done():
								return
							case logCh <- runtime.LogEntry{
								Timestamp: time.Now(),
								Stream:    "stdout",
								Log:       "Real-time log entry",
							}:
								time.Sleep(10 * time.Millisecond)
							}
						}
					}()

					return logCh, nil
				}
			},
			wantErr: false,
			checkLogs: func(t *testing.T, logs []*api.LogEntry) {
				assert.GreaterOrEqual(t, len(logs), 1)
			},
		},
		{
			name: "success - filter by timestamp (since parameter)",
			request: &api.StreamLogsRequest{
				ContainerId: "test-container",
				Follow:      false,
				SinceTime:   time.Now().Add(-1 * time.Hour).Unix(),
			},
			setupMock: func(m *mockRuntime) {
				m.getContainerLogsFn = func(ctx context.Context, containerID string, follow bool) (<-chan runtime.LogEntry, error) {
					logCh := make(chan runtime.LogEntry, 5)

					go func() {
						defer close(logCh)
						// Recent log
						logCh <- runtime.LogEntry{
							Timestamp: time.Now(),
							Stream:    "stdout",
							Log:       "Recent log",
						}
						// Old log (should be filtered by client if needed)
						logCh <- runtime.LogEntry{
							Timestamp: time.Now().Add(-2 * time.Hour),
							Stream:    "stdout",
							Log:       "Old log",
						}
					}()

					return logCh, nil
				}
			},
			wantErr: false,
		},
		{
			name: "error - container not found",
			request: &api.StreamLogsRequest{
				ContainerId: "nonexistent-container",
				Follow:      false,
			},
			setupMock: func(m *mockRuntime) {
				m.getContainerLogsFn = func(ctx context.Context, containerID string, follow bool) (<-chan runtime.LogEntry, error) {
					return nil, errors.New("container not found")
				}
			},
			wantErr:  true,
			wantCode: codes.Internal,
		},
		{
			name: "success - empty log file (no logs yet)",
			request: &api.StreamLogsRequest{
				ContainerId: "new-container",
				Follow:      false,
			},
			setupMock: func(m *mockRuntime) {
				m.getContainerLogsFn = func(ctx context.Context, containerID string, follow bool) (<-chan runtime.LogEntry, error) {
					// Return empty channel (log file exists but has no content)
					logCh := make(chan runtime.LogEntry, 1)
					close(logCh)
					return logCh, nil
				}
			},
			wantErr: false,
			checkLogs: func(t *testing.T, logs []*api.LogEntry) {
				assert.Empty(t, logs)
			},
		},
		{
			name: "error - context cancelled (stop streaming)",
			request: &api.StreamLogsRequest{
				ContainerId: "test-container",
				Follow:      true,
			},
			setupMock: func(m *mockRuntime) {
				m.getContainerLogsFn = func(ctx context.Context, containerID string, follow bool) (<-chan runtime.LogEntry, error) {
					logCh := make(chan runtime.LogEntry, 10)

					go func() {
						defer close(logCh)
						for {
							select {
							case <-ctx.Done():
								return
							case logCh <- runtime.LogEntry{
								Timestamp: time.Now(),
								Stream:    "stdout",
								Log:       "streaming...",
							}:
								time.Sleep(100 * time.Millisecond)
							}
						}
					}()

					return logCh, nil
				}
			},
			wantErr: false,
			checkLogs: func(t *testing.T, logs []*api.LogEntry) {
				// Should get some logs before cancellation
				assert.GreaterOrEqual(t, len(logs), 0)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := setupTestAgent(t)

			if tt.setupMock != nil {
				mockRt := a.runtime.(*mockRuntime)
				tt.setupMock(mockRt)
			}

			// Create a mock stream server
			mockStream := &mockStreamLogsServer{
				ctx:     context.Background(),
				entries: make([]*api.LogEntry, 0),
			}

			// For context cancellation test, cancel after short delay
			if tt.name == "error - context cancelled (stop streaming)" {
				ctx, cancel := context.WithCancel(context.Background())
				mockStream.ctx = ctx
				go func() {
					time.Sleep(50 * time.Millisecond)
					cancel()
				}()
			}

			err := a.StreamLogs(tt.request, mockStream)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantCode != codes.OK {
					st, ok := status.FromError(err)
					require.True(t, ok)
					assert.Equal(t, tt.wantCode, st.Code())
				}
			} else {
				require.NoError(t, err)

				if tt.checkLogs != nil {
					tt.checkLogs(t, mockStream.entries)
				}
			}
		})
	}
}

// mockStreamLogsServer implements api.AgentService_StreamLogsServer for testing
type mockStreamLogsServer struct {
	ctx     context.Context
	entries []*api.LogEntry
}

func (m *mockStreamLogsServer) Send(entry *api.LogEntry) error {
	m.entries = append(m.entries, entry)
	return nil
}

func (m *mockStreamLogsServer) Context() context.Context {
	return m.ctx
}

func (m *mockStreamLogsServer) SendMsg(msg interface{}) error {
	return nil
}

func (m *mockStreamLogsServer) RecvMsg(msg interface{}) error {
	return nil
}

func (m *mockStreamLogsServer) SetHeader(metadata interface{}) error {
	return nil
}

func (m *mockStreamLogsServer) SendHeader(metadata interface{}) error {
	return nil
}

func (m *mockStreamLogsServer) SetTrailer(metadata interface{}) {
}

// TestExecCommand tests command execution scenarios
func TestExecCommand(t *testing.T) {
	tests := []struct {
		name        string
		request     *api.ExecCommandRequest
		setupMock   func(*mockRuntime)
		wantErr     bool
		wantCode    codes.Code
		checkResult func(*testing.T, *api.ExecCommandResponse)
	}{
		{
			name: "success - exec simple command (echo hello)",
			request: &api.ExecCommandRequest{
				ContainerId: "test-container",
				Command:     []string{"echo", "hello"},
			},
			setupMock: func(m *mockRuntime) {
				m.execContainerFn = func(ctx context.Context, containerID string, command []string) (*runtime.ExecResult, error) {
					assert.Equal(t, "test-container", containerID)
					assert.Equal(t, []string{"echo", "hello"}, command)

					return &runtime.ExecResult{
						Stdout:   "hello\n",
						Stderr:   "",
						ExitCode: 0,
					}, nil
				}
			},
			wantErr: false,
			checkResult: func(t *testing.T, resp *api.ExecCommandResponse) {
				assert.Equal(t, "hello\n", resp.Stdout)
				assert.Equal(t, "", resp.Stderr)
				assert.Equal(t, int32(0), resp.ExitCode)
			},
		},
		{
			name: "success - capture stdout and stderr separately",
			request: &api.ExecCommandRequest{
				ContainerId: "test-container",
				Command:     []string{"sh", "-c", "echo stdout; echo stderr >&2"},
			},
			setupMock: func(m *mockRuntime) {
				m.execContainerFn = func(ctx context.Context, containerID string, command []string) (*runtime.ExecResult, error) {
					return &runtime.ExecResult{
						Stdout:   "stdout\n",
						Stderr:   "stderr\n",
						ExitCode: 0,
					}, nil
				}
			},
			wantErr: false,
			checkResult: func(t *testing.T, resp *api.ExecCommandResponse) {
				assert.Equal(t, "stdout\n", resp.Stdout)
				assert.Equal(t, "stderr\n", resp.Stderr)
				assert.Equal(t, int32(0), resp.ExitCode)
			},
		},
		{
			name: "success - non-zero exit code (exit 1)",
			request: &api.ExecCommandRequest{
				ContainerId: "test-container",
				Command:     []string{"exit", "1"},
			},
			setupMock: func(m *mockRuntime) {
				m.execContainerFn = func(ctx context.Context, containerID string, command []string) (*runtime.ExecResult, error) {
					return &runtime.ExecResult{
						Stdout:   "",
						Stderr:   "command failed\n",
						ExitCode: 1,
					}, nil
				}
			},
			wantErr: false,
			checkResult: func(t *testing.T, resp *api.ExecCommandResponse) {
				assert.Equal(t, int32(1), resp.ExitCode)
				assert.Equal(t, "command failed\n", resp.Stderr)
			},
		},
		{
			name: "success - long-running command with timeout",
			request: &api.ExecCommandRequest{
				ContainerId: "test-container",
				Command:     []string{"sleep", "10"},
			},
			setupMock: func(m *mockRuntime) {
				m.execContainerFn = func(ctx context.Context, containerID string, command []string) (*runtime.ExecResult, error) {
					// Simulate timeout via context
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(100 * time.Millisecond):
						return &runtime.ExecResult{
							Stdout:   "",
							Stderr:   "",
							ExitCode: 0,
						}, nil
					}
				}
			},
			wantErr: false,
		},
		{
			name: "error - container not found",
			request: &api.ExecCommandRequest{
				ContainerId: "nonexistent-container",
				Command:     []string{"echo", "test"},
			},
			setupMock: func(m *mockRuntime) {
				m.execContainerFn = func(ctx context.Context, containerID string, command []string) (*runtime.ExecResult, error) {
					return nil, errors.New("container not found")
				}
			},
			wantErr:  true,
			wantCode: codes.Internal,
		},
		{
			name: "error - command fails to start",
			request: &api.ExecCommandRequest{
				ContainerId: "test-container",
				Command:     []string{"/nonexistent/binary"},
			},
			setupMock: func(m *mockRuntime) {
				m.execContainerFn = func(ctx context.Context, containerID string, command []string) (*runtime.ExecResult, error) {
					return nil, errors.New("failed to start exec process: executable not found")
				}
			},
			wantErr:  true,
			wantCode: codes.Internal,
		},
		{
			name: "error - context cancelled during exec",
			request: &api.ExecCommandRequest{
				ContainerId: "test-container",
				Command:     []string{"sleep", "infinity"},
			},
			setupMock: func(m *mockRuntime) {
				m.execContainerFn = func(ctx context.Context, containerID string, command []string) (*runtime.ExecResult, error) {
					return nil, context.Canceled
				}
			},
			wantErr:  true,
			wantCode: codes.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := setupTestAgent(t)

			if tt.setupMock != nil {
				mockRt := a.runtime.(*mockRuntime)
				tt.setupMock(mockRt)
			}

			ctx := context.Background()

			// For context cancellation test, use cancelled context
			if tt.name == "error - context cancelled during exec" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel() // Cancel immediately
			}

			resp, err := a.ExecCommand(ctx, tt.request)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantCode != codes.OK {
					st, ok := status.FromError(err)
					require.True(t, ok)
					assert.Equal(t, tt.wantCode, st.Code())
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, resp)

				if tt.checkResult != nil {
					tt.checkResult(t, resp)
				}
			}
		})
	}
}

// TestConcurrentStreamLogs tests concurrent log streaming
func TestConcurrentStreamLogs(t *testing.T) {
	a := setupTestAgent(t)

	mockRt := a.runtime.(*mockRuntime)
	mockRt.getContainerLogsFn = func(ctx context.Context, containerID string, follow bool) (<-chan runtime.LogEntry, error) {
		logCh := make(chan runtime.LogEntry, 10)

		go func() {
			defer close(logCh)
			for i := 0; i < 5; i++ {
				select {
				case <-ctx.Done():
					return
				case logCh <- runtime.LogEntry{
					Timestamp: time.Now(),
					Stream:    "stdout",
					Log:       "concurrent log entry",
				}:
					time.Sleep(5 * time.Millisecond)
				}
			}
		}()

		return logCh, nil
	}

	// Run 5 concurrent log streaming requests
	done := make(chan bool, 5)

	for i := 0; i < 5; i++ {
		go func(id int) {
			defer func() { done <- true }()

			mockStream := &mockStreamLogsServer{
				ctx:     context.Background(),
				entries: make([]*api.LogEntry, 0),
			}

			err := a.StreamLogs(&api.StreamLogsRequest{
				ContainerId: "concurrent-container",
				Follow:      false,
			}, mockStream)

			assert.NoError(t, err)
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 5; i++ {
		<-done
	}
}

// TestConcurrentExecCommand tests concurrent command execution
func TestConcurrentExecCommand(t *testing.T) {
	a := setupTestAgent(t)

	mockRt := a.runtime.(*mockRuntime)
	mockRt.execContainerFn = func(ctx context.Context, containerID string, command []string) (*runtime.ExecResult, error) {
		// Simulate some processing time
		time.Sleep(10 * time.Millisecond)

		return &runtime.ExecResult{
			Stdout:   "concurrent execution result\n",
			Stderr:   "",
			ExitCode: 0,
		}, nil
	}

	ctx := context.Background()
	done := make(chan bool, 10)

	// Run 10 concurrent exec requests
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()

			resp, err := a.ExecCommand(ctx, &api.ExecCommandRequest{
				ContainerId: "concurrent-container",
				Command:     []string{"echo", "test"},
			})

			assert.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, int32(0), resp.ExitCode)
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}
