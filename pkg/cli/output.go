// Package cli implements the prisn command-line interface.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// OutputFormat represents the desired output format.
type OutputFormat string

const (
	OutputText  OutputFormat = "text"
	OutputJSON  OutputFormat = "json"
	OutputYAML  OutputFormat = "yaml"
	OutputTable OutputFormat = "table"
	OutputWide  OutputFormat = "wide"
)

// Printer handles formatting output based on the requested format.
type Printer struct {
	Format OutputFormat
	Writer io.Writer
}

// NewPrinter creates a printer from the command's output flag.
func NewPrinter(cmd *cobra.Command) *Printer {
	format, _ := cmd.Root().PersistentFlags().GetString("output")
	return &Printer{
		Format: OutputFormat(format),
		Writer: os.Stdout,
	}
}

// Print outputs the data in the appropriate format.
func (p *Printer) Print(data any) error {
	switch p.Format {
	case OutputJSON:
		return p.printJSON(data)
	case OutputYAML:
		return p.printYAML(data)
	case OutputTable, OutputText, OutputWide, "":
		// For table/text formats, the caller should handle formatting
		// This is a fallback that just prints as-is
		fmt.Fprintln(p.Writer, data)
		return nil
	default:
		return fmt.Errorf("unknown output format: %s (use text, json, yaml, or table)", p.Format)
	}
}

// printJSON outputs data as formatted JSON.
func (p *Printer) printJSON(data any) error {
	enc := json.NewEncoder(p.Writer)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

// printYAML outputs data as YAML.
func (p *Printer) printYAML(data any) error {
	return yaml.NewEncoder(p.Writer).Encode(data)
}

// IsStructured returns true if the output format expects structured data.
func (p *Printer) IsStructured() bool {
	return p.Format == OutputJSON || p.Format == OutputYAML
}

// PrintTable prints data as a formatted table.
// headers are the column names, rows are the data.
func (p *Printer) PrintTable(headers []string, rows [][]string) {
	if len(rows) == 0 {
		fmt.Fprintln(p.Writer, "(none)")
		return
	}

	w := tabwriter.NewWriter(p.Writer, 0, 0, 2, ' ', 0)

	// Print headers
	fmt.Fprintln(w, strings.Join(headers, "\t"))

	// Print rows
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}

	w.Flush()
}

// PrintList is a convenience method for printing a list of items.
// It handles structured formats automatically and calls tableFunc for text format.
func (p *Printer) PrintList(items any, tableFunc func()) error {
	if p.IsStructured() {
		return p.Print(map[string]any{
			"items": items,
			"count": getLen(items),
		})
	}
	tableFunc()
	return nil
}

// PrintItem is a convenience method for printing a single item.
func (p *Printer) PrintItem(item any, textFunc func()) error {
	if p.IsStructured() {
		return p.Print(item)
	}
	textFunc()
	return nil
}

// getLen returns the length of a slice using reflection.
func getLen(items any) int {
	v := reflect.ValueOf(items)
	if v.Kind() == reflect.Slice {
		return v.Len()
	}
	return 0
}

// getOutputFormat gets the output format from a cobra command.
func getOutputFormat(cmd *cobra.Command) OutputFormat {
	format, _ := cmd.Root().PersistentFlags().GetString("output")
	return OutputFormat(format)
}
