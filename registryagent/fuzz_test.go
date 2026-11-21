package registryagent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// FuzzValidate: arbitrary record payload bytes are refused or accepted
// quickly and without panicking; an accepted payload is an object with a
// supported schema.
func FuzzValidate(f *testing.F) {
	for _, s := range []string{
		``, `null`, `{}`, `[]`, `"x"`, `1e400`, `{"schema":"agent/1"}`,
		`{"schema":"agent/1","id":"a","name":"a","charter":"c","roles":["r"]}`,
		`{"schema":"agent/1","id":"a","name":"a","charter":"c","roles":[]}`,
		`{"schema":"agent/1","id":"","name":"a","charter":"c","roles":["r"]}`,
		`{"schema":"agent/1","id":1,"name":"a","charter":"c","roles":["r"]}`,
		`{"schema":"launch_profile/1","id":"p","name":"p","source":{"reference":{"authority":"tether","kind":"launch_profile","id":"s"},"revision":"r"},"runtime":{"settings":{"max_steps":1e300}}}`,
		`{"schema":"launch_profile/1","id":"p","name":"p","source":{"reference":{"authority":"tether","kind":"launch_profile","id":"s"},"revision":"r"},"runtime":{"settings":"x"}}`,
		`{"schema":"agent/1","schema":"agent/1"}`,
		`{"a":` + strings.Repeat(`[`, 40) + strings.Repeat(`]`, 40) + `}`,
		"{\"schema\":\"agent/1\",\"id\":\"\xff\",\"name\":\"a\",\"charter\":\"c\",\"roles\":[\"r\"]}",
		`{"schema":"agent/1","id":"a\u0000","name":"a","charter":"c","roles":["r"]} x`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		start := time.Now()
		obj, err := validate(context.Background(), raw)
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Fatalf("validate took %v", elapsed)
		}
		if err != nil {
			return
		}
		schema, _ := obj["schema"].(string)
		if _, ok := schemaFiles[schema]; !ok {
			t.Fatalf("unsupported schema %q accepted", schema)
		}
	})
}

// FuzzMaterializeProfile replaces the fixture's agent/1 (0) or
// launch_profile/1 (1) payload with fuzzed JSON. Materialize must return,
// with or without an error, and never panic.
func FuzzMaterializeProfile(f *testing.F) {
	f.Add(0, []byte(`{"schema":"agent/1","id":"fixture","name":"fixture","charter":"c","roles":["r"]}`))
	f.Add(0, []byte(`{"schema":"agent/1","id":"fixture","name":"fixture","charter":"\n","roles":["r"],"launch_profile":{"reference":{"authority":"registry","kind":"launch_profile","id":"profile"},"revision":"r1"}}`))
	f.Add(1, []byte(`{"schema":"launch_profile/1","id":"profile","name":"p","source":{"reference":{"authority":"tether","kind":"launch_profile","id":"s"},"revision":"r"},"host":"nanite-cli","runtime":{"settings":{}}}`))
	f.Add(1, []byte(`{"schema":"launch_profile/1","id":"profile","name":"p","source":{"reference":{"authority":"tether","kind":"launch_profile","id":"s"},"revision":"r"},"host":"nanite-cli","runtime":{"settings":{"max_steps":1e300}}}`))
	f.Add(1, []byte(`{"schema":"launch_profile/1","id":"profile","name":"p","source":{"reference":{"authority":"tether","kind":"launch_profile","id":"s"},"revision":"r"},"host":"nanite-service","runtime":{"model":"","settings":{"idle_timeout":"0s","compact_at":0,"context_window":0}}}`))
	f.Add(1, []byte(`{"schema":"launch_profile/1","id":"profile","name":"p","source":{"reference":{"authority":"tether","kind":"launch_profile","id":"s"},"revision":"r"}}`))
	f.Fuzz(func(t *testing.T, which int, raw []byte) {
		records, ref := fixture(t)
		which = ((which % 2) + 2) % 2
		records[which].Payload = json.RawMessage(raw)
		_, _ = Materialize(t.Context(), records, ref, filepath.Join(t.TempDir(), "agent"))
	})
}
