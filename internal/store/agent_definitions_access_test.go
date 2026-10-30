package store

import "testing"

func TestNativeHostSettings_AccessValidation(t *testing.T) {
	ok := NativeHostSettings{Version: "1", Runtime: "cli", Provider: "claude", Model: "m", Access: HostAccessBroad}
	if err := ok.Validate(); err != nil {
		t.Fatalf("broad cli claude: %v", err)
	}
	if !ok.Broad() || (&NativeHostSettings{}).Broad() {
		t.Fatal("Broad() wrong")
	}
	var nilHost *NativeHostSettings
	if nilHost.Broad() {
		t.Fatal("nil host must not be broad")
	}
	for name, h := range map[string]NativeHostSettings{
		"codex":   {Version: "1", Runtime: "cli", Provider: "codex", Model: "m", Access: HostAccessBroad},
		"api":     {Version: "1", Runtime: "api", Provider: "anthropic", Model: "m", Access: HostAccessBroad},
		"unknown": {Version: "1", Runtime: "cli", Provider: "claude", Model: "m", Access: "root"},
	} {
		if err := h.Validate(); err == nil {
			t.Errorf("%s: expected refusal", name)
		}
	}
}
