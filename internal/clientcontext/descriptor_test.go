package clientcontext

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func example() Descriptor {
	return Descriptor{Version: Version, View: View{
		Route:             "/docs",
		ActiveFilters:     []Filter{{Name: "status", Values: []string{"open"}}},
		Search:            "release",
		SelectedIDs:       []string{"doc-1"},
		VisibleRows:       []Row{{ID: "doc-1", Summary: "Release notes"}},
		AvailableCommands: []Command{{Name: "open_doc", Scope: "ephemeral", InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"default":null}`)}},
	}}
}

func encoded(t *testing.T, value Descriptor) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDescriptorSnapshotDetachedAndPrivate(t *testing.T) {
	raw := encoded(t, example())
	snapshot, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot.JSON()
	for i := range raw {
		raw[i] = 'x'
	}
	if snapshot.JSON() != before {
		t.Fatal("snapshot aliases mutable request bytes")
	}
	var roundtrip Descriptor
	if err = json.Unmarshal([]byte(before), &roundtrip); err != nil {
		t.Fatal(err)
	}
	if roundtrip.View.Route != "/docs" || roundtrip.View.VisibleRows[0].Summary != "Release notes" || roundtrip.View.AvailableCommands[0].Name != "open_doc" {
		t.Fatalf("view projection changed: %+v", roundtrip)
	}
	for _, rendered := range []string{fmt.Sprint(snapshot), fmt.Sprintf("%+v", snapshot), fmt.Sprintf("%#v", snapshot)} {
		if strings.Contains(rendered, "release") || strings.Contains(rendered, "doc-1") {
			t.Fatalf("accidental snapshot log exposes content: %s", rendered)
		}
	}
	logged, marshalErr := json.Marshal(snapshot)
	if marshalErr == nil || logged != nil {
		t.Fatalf("snapshot became serializable metadata: %s %v", logged, marshalErr)
	}
}

func TestDescriptorRefusesMalformedOrAuthorityFields(t *testing.T) {
	for name, raw := range map[string][]byte{
		"version alias":           []byte(`{"Version":1,"view":{"route":"/docs"}}`),
		"view alias":              []byte(`{"version":1,"View":{"route":"/docs"}}`),
		"nested alias":            []byte(`{"version":1,"view":{"route":"/docs","Search":"hidden"}}`),
		"row alias":               []byte(`{"version":1,"view":{"route":"/docs","visible_rows":[{"ID":"doc"}]}}`),
		"missing version":         []byte(`{"view":{"route":"/docs"}}`),
		"other version":           []byte(`{"version":2,"view":{"route":"/docs"}}`),
		"fractional version":      []byte(`{"version":1.0,"view":{"route":"/docs"}}`),
		"null root":               []byte(`null`),
		"null view":               []byte(`{"version":1,"view":null}`),
		"null search":             []byte(`{"version":1,"view":{"route":"/docs","search":null}}`),
		"null rows":               []byte(`{"version":1,"view":{"route":"/docs","visible_rows":null}}`),
		"unknown view":            []byte(`{"version":1,"view":{"route":"/docs","actor":"user"}}`),
		"unknown root":            []byte(`{"version":1,"view":{"route":"/docs"},"grants":["*"]}`),
		"duplicate root":          []byte(`{"version":1,"version":2,"view":{"route":"/docs"}}`),
		"duplicate nested":        []byte(`{"version":1,"view":{"route":"/docs","route":"/admin"}}`),
		"duplicate schema":        []byte(`{"version":1,"view":{"route":"/docs","available_commands":[{"name":"open_doc","scope":"ephemeral","input_schema":{"type":"object","type":"string"}}]}}`),
		"invalid UTF8":            append([]byte(`{"version":1,"view":{"route":"/docs","search":"`), append([]byte{0xff}, []byte(`"}}`)...)...),
		"unpaired high surrogate": []byte(`{"version":1,"view":{"route":"/docs","search":"\ud800"}}`),
		"unpaired low surrogate":  []byte(`{"version":1,"view":{"route":"/docs","search":"\udc00"}}`),
		"multiple documents":      []byte(`{"version":1,"view":{"route":"/docs"}} {}`),
		"raw byte limit":          bytes.Repeat([]byte(" "), MaxBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			value, err := Decode(raw)
			var invalid *ValidationError
			if err == nil || !errors.As(err, &invalid) || !value.Empty() {
				t.Fatalf("invalid input admitted: snapshot=%s err=%v", value, err)
			}
			if strings.Contains(err.Error(), "/admin") || strings.Contains(err.Error(), "grants") || strings.Contains(err.Error(), "actor") {
				t.Fatalf("validation error echoes client data: %v", err)
			}
		})
	}
}

func TestDescriptorUTF8EscapesAndExactSchemaNumbers(t *testing.T) {
	raw := []byte(`{"version":1,"view":{"route":"/docs","search":"\ud83d\ude80 café \\ud800","available_commands":[{"name":"navigate","scope":"url-backed","input_schema":{"type":"object","const":9007199254740993}}]}}`)
	snapshot, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot.JSON(), "9007199254740993") || !strings.Contains(snapshot.JSON(), "🚀") || !strings.Contains(snapshot.JSON(), `\\ud800`) {
		t.Fatalf("valid Unicode or schema number changed: %s", snapshot.JSON())
	}
}

func TestDescriptorFiniteCollectionsAndStrings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Descriptor)
	}{
		{"route", func(d *Descriptor) { d.View.Route = "/" + strings.Repeat("x", MaxRouteBytes) }},
		{"absolute route", func(d *Descriptor) { d.View.Route = "https://example.com/docs" }},
		{"network route", func(d *Descriptor) { d.View.Route = "//example.com/docs" }},
		{"route query", func(d *Descriptor) { d.View.Route = "/docs?credential=secret" }},
		{"route fragment", func(d *Descriptor) { d.View.Route = "/docs#private" }},
		{"empty fragment", func(d *Descriptor) { d.View.Route = "/docs#" }},
		{"encoded network route", func(d *Descriptor) { d.View.Route = "/%2fexample.com" }},
		{"route control", func(d *Descriptor) { d.View.Route = "/docs%0a" }},
		{"search", func(d *Descriptor) { d.View.Search = strings.Repeat("x", MaxSearchBytes+1) }},
		{"filters", func(d *Descriptor) { d.View.ActiveFilters = make([]Filter, MaxFilters+1) }},
		{"filter name", func(d *Descriptor) { d.View.ActiveFilters[0].Name = strings.Repeat("x", MaxFilterNameBytes+1) }},
		{"filter values", func(d *Descriptor) { d.View.ActiveFilters[0].Values = make([]string, MaxFilterValues+1) }},
		{"filter value", func(d *Descriptor) { d.View.ActiveFilters[0].Values[0] = strings.Repeat("x", MaxFilterValueBytes+1) }},
		{"duplicate filter", func(d *Descriptor) { d.View.ActiveFilters = append(d.View.ActiveFilters, d.View.ActiveFilters[0]) }},
		{"selected", func(d *Descriptor) { d.View.SelectedIDs = make([]string, MaxSelectedIDs+1) }},
		{"selected ID", func(d *Descriptor) { d.View.SelectedIDs[0] = strings.Repeat("x", MaxIDBytes+1) }},
		{"duplicate selected", func(d *Descriptor) { d.View.SelectedIDs = append(d.View.SelectedIDs, "doc-1") }},
		{"visible rows", func(d *Descriptor) { d.View.VisibleRows = make([]Row, MaxVisibleRows+1) }},
		{"summary", func(d *Descriptor) { d.View.VisibleRows[0].Summary = strings.Repeat("x", MaxSummaryBytes+1) }},
		{"duplicate row", func(d *Descriptor) { d.View.VisibleRows = append(d.View.VisibleRows, d.View.VisibleRows[0]) }},
		{"commands", func(d *Descriptor) { d.View.AvailableCommands = make([]Command, MaxCommands+1) }},
		{"command name", func(d *Descriptor) { d.View.AvailableCommands[0].Name = strings.Repeat("x", MaxCommandNameBytes+1) }},
		{"command scope", func(d *Descriptor) { d.View.AvailableCommands[0].Scope = "destructive" }},
		{"schema size", func(d *Descriptor) {
			d.View.AvailableCommands[0].InputSchema = json.RawMessage(`{"description":"` + strings.Repeat("x", MaxSchemaBytes) + `"}`)
		}},
		{"schema type", func(d *Descriptor) { d.View.AvailableCommands[0].InputSchema = json.RawMessage(`[]`) }},
		{"duplicate command", func(d *Descriptor) {
			d.View.AvailableCommands = append(d.View.AvailableCommands, d.View.AvailableCommands[0])
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := example()
			test.mutate(&value)
			if _, err := Decode(encoded(t, value)); err == nil {
				t.Fatal("out-of-contract observation admitted")
			}
		})
	}
	value := example()
	value.View.Search = strings.Repeat("é", MaxSearchBytes/2)
	value.View.VisibleRows[0].Summary = strings.Repeat("x", MaxSummaryBytes)
	if _, err := Decode(encoded(t, value)); err != nil {
		t.Fatalf("byte boundaries refused: %v", err)
	}
}

func TestDescriptorNormalizedByteDepthAndNodeBudgets(t *testing.T) {
	value := example()
	value.View.VisibleRows = nil
	for i := 0; i < MaxVisibleRows; i++ {
		value.View.VisibleRows = append(value.View.VisibleRows, Row{ID: fmt.Sprint(i), Summary: strings.Repeat("<", MaxSummaryBytes)})
	}
	raw := bytes.ReplaceAll(encoded(t, value), []byte(`\u003c`), []byte("<"))
	if len(raw) >= MaxBytes {
		t.Fatal("fixture must fit raw budget")
	}
	if _, err := Decode(raw); err == nil {
		t.Fatal("escaping expansion exceeded the prompt byte budget")
	}
	deep := []byte(`{"version":1,"view":{"route":"/docs","available_commands":[{"name":"x","scope":"ephemeral","input_schema":{"x":` + strings.Repeat("[", MaxDepth) + `0` + strings.Repeat("]", MaxDepth) + `}}]}}`)
	if _, err := Decode(deep); err == nil {
		t.Fatal("deep schema admitted")
	}
	nodes := []byte(`{"version":1,"view":{"route":"/docs","available_commands":[{"name":"x","scope":"ephemeral","input_schema":{"x":[` + strings.Repeat("0,", MaxNodes) + `0]}}]}}`)
	if len(nodes) > MaxBytes {
		t.Fatal("fixture must fit raw budget")
	}
	if _, err := Decode(nodes); err == nil {
		t.Fatal("excessive schema nodes admitted")
	}
}
