// Package output owns all rendering to stdout. Diagnostics and errors are
// rendered by the CLI to stderr.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/timbrinded/freeagent-cli/internal/protocol"
)

type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatJSONL Format = "jsonl"
	FormatRaw   Format = "raw"
)

func ParseFormat(value string) (Format, error) {
	format := Format(strings.ToLower(strings.TrimSpace(value)))
	switch format {
	case FormatTable, FormatJSON, FormatJSONL, FormatRaw:
		return format, nil
	default:
		return "", fmt.Errorf("unsupported output format %q; use table, json, jsonl, or raw", value)
	}
}

type Renderer struct {
	Writer io.Writer
}

func (r Renderer) Success(format Format, data any, meta protocol.Meta) error {
	if r.Writer == nil {
		return fmt.Errorf("output writer is nil")
	}
	switch format {
	case FormatJSON:
		return writeJSON(r.Writer, protocol.SuccessEnvelope{ProtocolVersion: protocol.Version, OK: true, Data: data, Meta: meta})
	case FormatJSONL:
		return r.writeJSONL(data, meta)
	case FormatRaw:
		return writeRaw(r.Writer, data)
	case FormatTable:
		return writeTable(r.Writer, data)
	default:
		return fmt.Errorf("unsupported output format %q", format)
	}
}

func (r Renderer) Error(format Format, detail protocol.ErrorDetail, meta protocol.Meta) error {
	if r.Writer == nil {
		return fmt.Errorf("error writer is nil")
	}
	if format == FormatTable {
		_, err := fmt.Fprintf(r.Writer, "error: %s\n", detail.Message)
		return err
	}
	return writeJSON(r.Writer, protocol.ErrorEnvelope{ProtocolVersion: protocol.Version, OK: false, Error: detail, Meta: meta})
}

func (r Renderer) writeJSONL(data any, meta protocol.Meta) error {
	items := toItems(data)
	encoder := json.NewEncoder(r.Writer)
	encoder.SetEscapeHTML(false)
	for _, item := range items {
		record := struct {
			ProtocolVersion string `json:"protocol_version"`
			Type            string `json:"type"`
			Data            any    `json:"data"`
		}{ProtocolVersion: protocol.Version, Type: "item", Data: item}
		if err := encoder.Encode(record); err != nil {
			return err
		}
	}
	// A partial stream has no success summary. The executor writes its error to
	// stderr and returns ExitPartial.
	if meta.Complete != nil && !*meta.Complete {
		return nil
	}
	if meta.Items == 0 {
		meta.Items = len(items)
	}
	summary := struct {
		ProtocolVersion string        `json:"protocol_version"`
		Type            string        `json:"type"`
		Meta            protocol.Meta `json:"meta"`
	}{ProtocolVersion: protocol.Version, Type: "summary", Meta: meta}
	return encoder.Encode(summary)
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func writeRaw(writer io.Writer, data any) error {
	var raw []byte
	switch value := data.(type) {
	case nil:
		raw = []byte("null")
	case []byte:
		raw = value
	case json.RawMessage:
		raw = value
	case string:
		raw = []byte(value)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		raw = encoded
	}
	_, err := writer.Write(raw)
	return err
}

func writeTable(writer io.Writer, data any) error {
	rows, columns := tableRows(data)
	if len(columns) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	for index, column := range columns {
		if index > 0 {
			_, _ = io.WriteString(tw, "\t")
		}
		_, _ = io.WriteString(tw, safeTableText(strings.ToUpper(column)))
	}
	_, _ = io.WriteString(tw, "\n")
	for _, row := range rows {
		for index, column := range columns {
			if index > 0 {
				_, _ = io.WriteString(tw, "\t")
			}
			_, _ = io.WriteString(tw, tableCell(row[column]))
		}
		_, _ = io.WriteString(tw, "\n")
	}
	return tw.Flush()
}

func tableRows(data any) ([]map[string]any, []string) {
	items := toItems(data)
	rows := make([]map[string]any, 0, len(items))
	columnSet := make(map[string]struct{})
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			row = map[string]any{"value": item}
		}
		rows = append(rows, row)
		for key := range row {
			columnSet[key] = struct{}{}
		}
	}
	columns := make([]string, 0, len(columnSet))
	for column := range columnSet {
		columns = append(columns, column)
	}
	sort.Strings(columns)
	return rows, columns
}

func toItems(data any) []any {
	switch value := data.(type) {
	case []any:
		return value
	case []map[string]any:
		items := make([]any, len(value))
		for index := range value {
			items[index] = value[index]
		}
		return items
	case nil:
		return nil
	default:
		normalized, ok := normalize(data)
		if !ok {
			return []any{data}
		}
		if items, ok := normalized.([]any); ok {
			return items
		}
		return []any{normalized}
	}
}

func normalize(value any) (any, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, false
	}
	return normalized, true
}

func tableCell(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return safeTableText(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		var buffer bytes.Buffer
		encoder := json.NewEncoder(&buffer)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(value); err != nil {
			return fmt.Sprint(value)
		}
		return strings.TrimSuffix(buffer.String(), "\n")
	}
}

func safeTableText(value string) string {
	var result strings.Builder
	result.Grow(len(value))
	for _, character := range value {
		if character < 0x20 || character >= 0x7f && character <= 0x9f {
			_, _ = fmt.Fprintf(&result, "\\u%04X", character)
			continue
		}
		result.WriteRune(character)
	}
	return result.String()
}
