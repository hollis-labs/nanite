package service

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/tool/broker"
)

// TestExtraSystemPrefix_IncludesOverrideBlock verifies that when a Selection
// carries a non-empty OverrideBlock, the composed prefix includes it AFTER
// the Native Tool Usage guide (so tool-specific overrides override the
// general guidance).
func TestExtraSystemPrefix_IncludesOverrideBlock(t *testing.T) {
	sel := broker.Selection{
		ToolNames:     []string{"tool_a"},
		OverrideBlock: "## Tool Overrides\n\n- **tool_a**: returns widgets\n",
	}
	prefix := composeExtraSystemPrefix(sel, composeConfig{progressiveActive: false, noTools: false})
	if !strings.Contains(prefix, "## Tool Overrides") {
		t.Errorf("prefix missing override block:\n%s", prefix)
	}
	if !strings.Contains(prefix, "Native Tool Usage") {
		t.Errorf("prefix missing native tool guide:\n%s", prefix)
	}
	// Ensure override block comes AFTER native tool guide.
	nativeIdx := strings.Index(prefix, "Native Tool Usage")
	overrideIdx := strings.Index(prefix, "Tool Overrides")
	if overrideIdx < nativeIdx {
		t.Errorf("expected Tool Overrides to appear AFTER Native Tool Usage in prefix; native=%d override=%d", nativeIdx, overrideIdx)
	}
}

// TestExtraSystemPrefix_OmitsEmptyOverrideBlock verifies that when Selection's
// OverrideBlock is empty, no override section is emitted.
func TestExtraSystemPrefix_OmitsEmptyOverrideBlock(t *testing.T) {
	sel := broker.Selection{
		ToolNames:     []string{"tool_a"},
		OverrideBlock: "",
	}
	prefix := composeExtraSystemPrefix(sel, composeConfig{progressiveActive: false, noTools: false})
	if strings.Contains(prefix, "Tool Overrides") {
		t.Errorf("prefix unexpectedly contains override section:\n%s", prefix)
	}
	if !strings.Contains(prefix, "Native Tool Usage") {
		t.Errorf("prefix missing native tool guide:\n%s", prefix)
	}
}

// TestExtraSystemPrefix_NoToolsWarning verifies the warning prefix is emitted
// when noTools is true, and absent otherwise.
func TestExtraSystemPrefix_NoToolsWarning(t *testing.T) {
	sel := broker.Selection{}

	with := composeExtraSystemPrefix(sel, composeConfig{noTools: true})
	if !strings.Contains(with, "no tools available in this session") {
		t.Errorf("expected no-tools warning when noTools=true, got:\n%s", with)
	}

	without := composeExtraSystemPrefix(sel, composeConfig{noTools: false})
	if strings.Contains(without, "no tools available in this session") {
		t.Errorf("did not expect no-tools warning when noTools=false, got:\n%s", without)
	}
}

// TestExtraSystemPrefix_ProgressiveCatalog verifies the progressive catalog
// is included before the native tool guide when progressiveActive is true.
func TestExtraSystemPrefix_ProgressiveCatalog(t *testing.T) {
	sel := broker.Selection{}
	catalog := "## Available Tools (summary)\n- foo: does foo stuff\n"
	prefix := composeExtraSystemPrefix(sel, composeConfig{
		progressiveActive:  true,
		progressiveCatalog: catalog,
	})
	if !strings.Contains(prefix, "Available Tools (summary)") {
		t.Errorf("expected progressive catalog in prefix, got:\n%s", prefix)
	}
	catIdx := strings.Index(prefix, "Available Tools (summary)")
	nativeIdx := strings.Index(prefix, "Native Tool Usage")
	if catIdx > nativeIdx {
		t.Errorf("expected catalog BEFORE native tool guide; catalog=%d native=%d", catIdx, nativeIdx)
	}
}
