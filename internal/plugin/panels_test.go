package plugin

import (
	"net/http"
	"testing"
)

func TestPanelOwnershipAcrossCorePanelsAndRailSlots(t *testing.T) {
	host := NewHost(http.NewServeMux(), NewLogger("test"))
	for _, id := range []string{"widgets", "work", "workflows", "inbox", "artifacts"} {
		if err := host.RegisterPanel(PanelEntry{ID: id, PluginID: "claimant", Component: "View"}); err == nil {
			t.Fatalf("plugin displaced core panel %s", id)
		}
		if err := host.RegisterSlot(UISlotEntry{ID: id, Slot: "right-rail-tab", PluginID: "claimant", Component: "View", Label: "Claim"}); err == nil {
			t.Fatalf("slot displaced core panel %s", id)
		}
	}
	if err := host.RegisterPanel(PanelEntry{ID: "owned", PluginID: "first", Component: "First"}); err != nil {
		t.Fatal(err)
	}
	if err := host.RegisterSlot(UISlotEntry{ID: "owned", Slot: "right-rail-tab", PluginID: "second", Component: "Second", Label: "Claim"}); err == nil {
		t.Fatal("slot displaced another plugin's panel")
	}
	if err := host.RegisterSlot(UISlotEntry{ID: "slot-owned", Slot: "right-rail-tab", PluginID: "first", Component: "First", Label: "Slot"}); err != nil {
		t.Fatal(err)
	}
	if err := host.RegisterPanel(PanelEntry{ID: "slot-owned", PluginID: "second", Component: "Second"}); err == nil {
		t.Fatal("panel displaced another plugin's slot")
	}
	before := host.RegistryVersion()
	if err := host.RegisterPanel(PanelEntry{ID: "owned", PluginID: "first", Component: "Updated"}); err != nil {
		t.Fatal(err)
	}
	if host.RegistryVersion() <= before {
		t.Fatal("panel update did not invalidate browser response")
	}
	for _, panel := range host.GetPanels() {
		if panel.ID == "owned" && panel.Component != "Updated" {
			t.Fatalf("panel export = %s", panel.Component)
		}
	}
	if count := host.UnregisterPluginPanels("first"); count != 1 {
		t.Fatalf("removed %d panels", count)
	}
}
