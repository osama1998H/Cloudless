package config

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestNewOutputter verifies outputter creation with different formats.
// Satisfies GO_ENGINEERING_SOP.md §12.2 (table-driven tests).
func TestNewOutputter(t *testing.T) {
	tests := []struct {
		name           string
		format         string
		expectedFormat OutputFormat
	}{
		{
			name:           "json format",
			format:         "json",
			expectedFormat: OutputJSON,
		},
		{
			name:           "yaml format",
			format:         "yaml",
			expectedFormat: OutputYAML,
		},
		{
			name:           "table format",
			format:         "table",
			expectedFormat: OutputTable,
		},
		{
			name:           "empty format defaults to empty string",
			format:         "",
			expectedFormat: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := NewOutputter(tt.format)

			if out == nil {
				t.Fatal("NewOutputter returned nil")
			}

			if out.GetFormat() != tt.expectedFormat {
				t.Errorf("GetFormat() = %v, want %v", out.GetFormat(), tt.expectedFormat)
			}

			if out.writer == nil {
				t.Error("writer should not be nil")
			}
		})
	}
}

// TestOutputter_PrintJSON tests JSON output formatting.
// Verifies CLD-REQ-072 JSON output requirement.
func TestOutputter_PrintJSON(t *testing.T) {
	tests := []struct {
		name     string
		data     interface{}
		wantJSON string
		wantErr  bool
	}{
		{
			name: "simple struct",
			data: struct {
				Name  string `json:"name"`
				Value int    `json:"value"`
			}{
				Name:  "test",
				Value: 42,
			},
			wantJSON: `{
  "name": "test",
  "value": 42
}
`,
			wantErr: false,
		},
		{
			name: "array of structs",
			data: []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}{
				{ID: "1", Name: "first"},
				{ID: "2", Name: "second"},
			},
			wantJSON: `[
  {
    "id": "1",
    "name": "first"
  },
  {
    "id": "2",
    "name": "second"
  }
]
`,
			wantErr: false,
		},
		{
			name: "map data",
			data: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
			// Note: map order is not guaranteed in JSON, so we'll check differently
			wantErr: false,
		},
		{
			name:     "nil data",
			data:     nil,
			wantJSON: "null\n",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			out := &Outputter{
				format: OutputJSON,
				writer: &buf,
			}

			err := out.Print(tt.data)

			if (err != nil) != tt.wantErr {
				t.Errorf("Print() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			// For map test, just verify it's valid JSON
			if tt.name == "map data" {
				var result map[string]string
				if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
					t.Errorf("output is not valid JSON: %v", err)
				}
				return
			}

			got := buf.String()
			if got != tt.wantJSON {
				t.Errorf("Print() output mismatch:\ngot:\n%s\nwant:\n%s", got, tt.wantJSON)
			}
		})
	}
}

// TestOutputter_PrintYAML tests YAML output formatting.
// Verifies CLD-REQ-072 YAML output requirement.
func TestOutputter_PrintYAML(t *testing.T) {
	tests := []struct {
		name     string
		data     interface{}
		wantYAML string
		wantErr  bool
	}{
		{
			name: "simple struct",
			data: struct {
				Name  string `yaml:"name"`
				Value int    `yaml:"value"`
			}{
				Name:  "test",
				Value: 42,
			},
			wantYAML: "name: test\nvalue: 42\n",
			wantErr:  false,
		},
		{
			name: "nested struct",
			data: struct {
				Name   string `yaml:"name"`
				Config struct {
					Host string `yaml:"host"`
					Port int    `yaml:"port"`
				} `yaml:"config"`
			}{
				Name: "service",
				Config: struct {
					Host string `yaml:"host"`
					Port int    `yaml:"port"`
				}{
					Host: "localhost",
					Port: 8080,
				},
			},
			wantYAML: "name: service\nconfig:\n  host: localhost\n  port: 8080\n",
			wantErr:  false,
		},
		{
			name: "array",
			data: []string{"item1", "item2", "item3"},
			wantYAML: "- item1\n- item2\n- item3\n",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			out := &Outputter{
				format: OutputYAML,
				writer: &buf,
			}

			err := out.Print(tt.data)

			if (err != nil) != tt.wantErr {
				t.Errorf("Print() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			got := buf.String()
			if got != tt.wantYAML {
				t.Errorf("Print() output mismatch:\ngot:\n%s\nwant:\n%s", got, tt.wantYAML)
			}
		})
	}
}

// TestOutputter_PrintTable tests table output formatting.
// Verifies CLD-REQ-072 table output requirement.
func TestOutputter_PrintTable(t *testing.T) {
	tests := []struct {
		name         string
		headers      []string
		rows         [][]string
		wantContains []string // Strings that should appear in output
	}{
		{
			name:    "simple table",
			headers: []string{"ID", "NAME", "STATUS"},
			rows: [][]string{
				{"1", "node-1", "active"},
				{"2", "node-2", "inactive"},
			},
			wantContains: []string{
				"ID", "NAME", "STATUS",
				"node-1", "active",
				"node-2", "inactive",
			},
		},
		{
			name:    "empty table",
			headers: []string{"COL1", "COL2"},
			rows:    [][]string{},
			wantContains: []string{
				"COL1", "COL2",
			},
		},
		{
			name:    "table with many columns",
			headers: []string{"A", "B", "C", "D", "E"},
			rows: [][]string{
				{"1", "2", "3", "4", "5"},
			},
			wantContains: []string{"A", "B", "C", "D", "E", "1", "2", "3", "4", "5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			out := &Outputter{
				format: OutputTable,
				writer: &buf,
			}

			out.PrintTable(tt.headers, tt.rows)

			output := buf.String()

			// Verify all expected strings appear in output (with flexible matching for formatted headers)
			for _, want := range tt.wantContains {
				// The table library may format headers with spaces (e.g., "COL1" -> "COL 1")
				if !strings.Contains(output, want) && !strings.Contains(strings.ReplaceAll(output, " ", ""), want) {
					t.Errorf("PrintTable() output missing %q:\n%s", want, output)
				}
			}

			// Verify table structure (should have Unicode box-drawing or ASCII border characters)
			hasUnicode := strings.ContainsAny(output, "┌├└│─┬┴┼")
			hasASCII := strings.Contains(output, "+") || strings.Contains(output, "|")
			if !hasUnicode && !hasASCII {
				t.Error("PrintTable() output doesn't look like a table")
			}
		})
	}
}

// TestOutputter_PrintTableFormat tests that table format requires custom handling.
func TestOutputter_PrintTableFormat(t *testing.T) {
	out := &Outputter{
		format: OutputTable,
		writer: &bytes.Buffer{},
	}

	// Table format should return error when using generic Print()
	err := out.Print(map[string]string{"key": "value"})

	if err == nil {
		t.Error("Print() with table format should return error")
	}

	expectedErr := "table format requires custom formatting"
	if err.Error() != expectedErr {
		t.Errorf("Print() error = %q, want %q", err.Error(), expectedErr)
	}
}

// TestOutputter_PrintUnknownFormat tests handling of unknown format.
func TestOutputter_PrintUnknownFormat(t *testing.T) {
	out := &Outputter{
		format: "invalid",
		writer: &bytes.Buffer{},
	}

	err := out.Print(map[string]string{"key": "value"})

	if err == nil {
		t.Error("Print() with unknown format should return error")
	}

	if !strings.Contains(err.Error(), "unknown output format") {
		t.Errorf("Print() error = %q, want error containing 'unknown output format'", err.Error())
	}
}

// TestOutputter_GetFormat tests format retrieval.
func TestOutputter_GetFormat(t *testing.T) {
	tests := []struct {
		name   string
		format OutputFormat
	}{
		{"json", OutputJSON},
		{"yaml", OutputYAML},
		{"table", OutputTable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &Outputter{
				format: tt.format,
				writer: &bytes.Buffer{},
			}

			if got := out.GetFormat(); got != tt.format {
				t.Errorf("GetFormat() = %v, want %v", got, tt.format)
			}
		})
	}
}

// TestOutputFormats tests all output format constants.
func TestOutputFormats(t *testing.T) {
	tests := []struct {
		name   string
		format OutputFormat
		want   string
	}{
		{"table constant", OutputTable, "table"},
		{"json constant", OutputJSON, "json"},
		{"yaml constant", OutputYAML, "yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.format) != tt.want {
				t.Errorf("OutputFormat = %q, want %q", tt.format, tt.want)
			}
		})
	}
}

// BenchmarkOutputter_PrintJSON benchmarks JSON output.
// Per GO_ENGINEERING_SOP.md §13.4.
func BenchmarkOutputter_PrintJSON(b *testing.B) {
	data := struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
		Value  int    `json:"value"`
	}{
		ID:     "test-id",
		Name:   "test-name",
		Status: "active",
		Value:  42,
	}

	out := &Outputter{
		format: OutputJSON,
		writer: &bytes.Buffer{},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = out.Print(data)
	}
}

// BenchmarkOutputter_PrintYAML benchmarks YAML output.
func BenchmarkOutputter_PrintYAML(b *testing.B) {
	data := struct {
		ID     string `yaml:"id"`
		Name   string `yaml:"name"`
		Status string `yaml:"status"`
		Value  int    `yaml:"value"`
	}{
		ID:     "test-id",
		Name:   "test-name",
		Status: "active",
		Value:  42,
	}

	out := &Outputter{
		format: OutputYAML,
		writer: &bytes.Buffer{},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = out.Print(data)
	}
}
