package plugin

import "context"

// CoreDataAdopter transfers a retired first-party feature only after its
// reviewed subprocess and all manifest registrations have successfully loaded.
// It is host-owned policy: an arbitrary plugin cannot choose a core table.
type CoreDataAdopter interface {
	AdoptPluginCoreData(context.Context, string) error
}

func (h *Host) SetCoreDataAdopter(adopter CoreDataAdopter) {
	h.mu.Lock()
	h.coreDataAdopter = adopter
	h.mu.Unlock()
}
