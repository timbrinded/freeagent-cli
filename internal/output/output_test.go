package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/timbrinded/freeagent-cli/internal/protocol"
)

func TestRawPreservesBytesExactly(t *testing.T) {
	t.Parallel()
	want := []byte{0x25, 0x50, 0x44, 0x46, 0x00, 0xff}
	var buffer bytes.Buffer
	if err := (Renderer{Writer: &buffer}).Success(FormatRaw, want, protocol.Meta{}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buffer.Bytes(), want) {
		t.Fatalf("raw bytes changed: got %v want %v", buffer.Bytes(), want)
	}
}

func TestJSONSuccessEnvelope(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	if err := (Renderer{Writer: &buffer}).Success(FormatJSON, map[string]any{"id": "1"}, protocol.Meta{Command: "contacts.get"}); err != nil {
		t.Fatal(err)
	}
	var envelope protocol.SuccessEnvelope
	if err := json.Unmarshal(buffer.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.ProtocolVersion != "1" || !envelope.OK || envelope.Meta.Command != "contacts.get" {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
}

func TestJSONLPartialHasNoSuccessSummary(t *testing.T) {
	t.Parallel()
	complete := false
	var buffer bytes.Buffer
	if err := (Renderer{Writer: &buffer}).Success(FormatJSONL, []any{map[string]any{"id": 1}}, protocol.Meta{Complete: &complete}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buffer.String(), `"type":"summary"`) {
		t.Fatalf("partial stream contained a success summary: %s", buffer.String())
	}
	if !strings.Contains(buffer.String(), `"type":"item"`) {
		t.Fatalf("partial stream omitted its item: %s", buffer.String())
	}
}

func TestTableColumnsAreDeterministic(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	data := []map[string]any{{"z": 2, "a": 1}}
	if err := (Renderer{Writer: &buffer}).Success(FormatTable, data, protocol.Meta{}); err != nil {
		t.Fatal(err)
	}
	if first := strings.Split(buffer.String(), "\n")[0]; !strings.HasPrefix(first, "A") || !strings.Contains(first, "Z") {
		t.Fatalf("columns were not sorted: %q", first)
	}
}

func TestTableEscapesTerminalControlsInHeadersAndValues(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	data := []map[string]any{{"bad\x1b]52;c;payload\a": "a\tb\rc\x1b]52;c;payload\a\u0085"}}
	if err := (Renderer{Writer: &buffer}).Success(FormatTable, data, protocol.Meta{}); err != nil {
		t.Fatal(err)
	}
	output := buffer.String()
	for _, forbidden := range []string{"\x1b", "\a", "\r", "\u0085"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("table contains raw terminal control %q: %q", forbidden, output)
		}
	}
	for _, escaped := range []string{`\u001B`, `\u0007`, `\u0009`, `\u000D`, `\u0085`} {
		if !strings.Contains(output, escaped) {
			t.Fatalf("table omitted escaped control %q: %q", escaped, output)
		}
	}
}
