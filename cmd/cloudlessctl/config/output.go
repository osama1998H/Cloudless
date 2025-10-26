package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/olekukonko/tablewriter"
	"gopkg.in/yaml.v3"
)

// OutputFormat represents the output format
type OutputFormat string

const (
	OutputTable OutputFormat = "table"
	OutputJSON  OutputFormat = "json"
	OutputYAML  OutputFormat = "yaml"
)

// Outputter handles formatted output
type Outputter struct {
	format OutputFormat
	writer io.Writer
}

// NewOutputter creates a new outputter
func NewOutputter(format string) *Outputter {
	return &Outputter{
		format: OutputFormat(format),
		writer: os.Stdout,
	}
}

// Print outputs data in the configured format
func (o *Outputter) Print(data interface{}) error {
	switch o.format {
	case OutputJSON:
		return o.printJSON(data)
	case OutputYAML:
		return o.printYAML(data)
	case OutputTable:
		// Table format requires custom handling per data type
		return fmt.Errorf("table format requires custom formatting")
	default:
		return fmt.Errorf("unknown output format: %s", o.format)
	}
}

// PrintTable prints data as a table
func (o *Outputter) PrintTable(headers []string, rows [][]string) {
	table := tablewriter.NewWriter(o.writer)

	// Convert []string to []any for Header
	headerAny := make([]any, len(headers))
	for i, h := range headers {
		headerAny[i] = h
	}
	table.Header(headerAny...)

	for _, row := range rows {
		table.Append(row)
	}
	table.Render()
}

// printJSON outputs data as JSON
func (o *Outputter) printJSON(data interface{}) error {
	encoder := json.NewEncoder(o.writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

// printYAML outputs data as YAML
func (o *Outputter) printYAML(data interface{}) error {
	encoder := yaml.NewEncoder(o.writer)
	encoder.SetIndent(2)
	defer encoder.Close()
	return encoder.Encode(data)
}

// GetFormat returns the output format
func (o *Outputter) GetFormat() OutputFormat {
	return o.format
}
