package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hollis-labs/nanite/internal/bootprofile"
	"gopkg.in/yaml.v3"
)

var metaHarnessIDPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

type metaHarnessRecord struct {
	ID            string            `json:"id"`
	DisplayName   string            `json:"display_name"`
	Launch        string            `json:"launch"`
	UILabel       string            `json:"ui_label"`
	Provider      string            `json:"provider"`
	ProviderAlias string            `json:"provider_alias,omitempty"`
	Workdir       string            `json:"workdir"`
	BootMode      string            `json:"boot_mode,omitempty"`
	Args          []string          `json:"args"`
	Env           map[string]string `json:"env"`
	Role          string            `json:"role,omitempty"`
	Project       string            `json:"project,omitempty"`
	WorkRoot      string            `json:"work_root,omitempty"`
	TrackingRoot  string            `json:"tracking_root,omitempty"`
	MCPServers    []string          `json:"mcp_servers"`
	ProfilePath   string            `json:"profile_path"`
	LaunchPath    string            `json:"launch_path"`
}

type metaHarnessRequest struct {
	ID           string            `json:"id"`
	DisplayName  string            `json:"display_name"`
	UILabel      string            `json:"ui_label"`
	Provider     string            `json:"provider"`
	Workdir      string            `json:"workdir"`
	BootMode     string            `json:"boot_mode,omitempty"`
	Args         []string          `json:"args"`
	Env          map[string]string `json:"env"`
	Role         string            `json:"role,omitempty"`
	Project      string            `json:"project,omitempty"`
	WorkRoot     string            `json:"work_root,omitempty"`
	TrackingRoot string            `json:"tracking_root,omitempty"`
	MCPServers   []string          `json:"mcp_servers"`
}

func (a *API) handleListMetaHarnesses(w http.ResponseWriter, r *http.Request) {
	root, err := a.metaHarnessCatalogRoot()
	if err != nil {
		a.errorResp(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	cat, err := bootprofile.LoadCatalog(root)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	records := make([]metaHarnessRecord, 0, len(cat.Profiles))
	for id, profile := range cat.Profiles {
		launch := cat.Launches[profile.Launch]
		records = append(records, metaHarnessRecordFromCatalog(root, id, profile, launch))
	}
	sort.Slice(records, func(i, j int) bool {
		left := firstNonEmpty(records[i].UILabel, records[i].DisplayName, records[i].ID)
		right := firstNonEmpty(records[j].UILabel, records[j].DisplayName, records[j].ID)
		return strings.ToLower(left) < strings.ToLower(right)
	})
	a.jsonResp(w, http.StatusOK, nonNilSlice(records))
}

func (a *API) handleCreateMetaHarness(w http.ResponseWriter, r *http.Request) {
	var req metaHarnessRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	record, err := a.saveMetaHarness(req, false)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, record)
}

func (a *API) handleUpdateMetaHarness(w http.ResponseWriter, r *http.Request) {
	var req metaHarnessRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	req.ID = r.PathValue("id")
	record, err := a.saveMetaHarness(req, true)
	if errors.Is(err, os.ErrNotExist) {
		a.errorResp(w, http.StatusNotFound, "meta harness not found")
		return
	}
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, record)
}

func (a *API) handleDeleteMetaHarness(w http.ResponseWriter, r *http.Request) {
	root, err := a.metaHarnessCatalogRoot()
	if err != nil {
		a.errorResp(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if err := validateMetaHarnessID(id); err != nil {
		a.errorResp(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.Remove(metaHarnessProfilePath(root, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.Remove(metaHarnessLaunchPath(root, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.reloadMetaHarnessRegistry()
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) saveMetaHarness(req metaHarnessRequest, update bool) (metaHarnessRecord, error) {
	root, err := a.metaHarnessCatalogRoot()
	if err != nil {
		return metaHarnessRecord{}, err
	}
	id := strings.TrimSpace(req.ID)
	if err := validateMetaHarnessID(id); err != nil {
		return metaHarnessRecord{}, err
	}
	profilePath := metaHarnessProfilePath(root, id)
	launchPath := metaHarnessLaunchPath(root, id)
	if update {
		if _, err := os.Stat(profilePath); err != nil {
			return metaHarnessRecord{}, err
		}
	}
	cat, _ := bootprofile.LoadCatalog(root)
	profile := cat.Profiles[id]
	launch := cat.Launches[id]
	if profile.ID == "" {
		profile = defaultMetaHarnessProfile(id)
	}
	if launch.ID == "" {
		launch = bootprofile.Launch{ID: id}
	}
	applyMetaHarnessRequest(&profile, &launch, req)

	if err := os.MkdirAll(filepath.Dir(profilePath), 0o755); err != nil {
		return metaHarnessRecord{}, err
	}
	if err := os.MkdirAll(filepath.Dir(launchPath), 0o755); err != nil {
		return metaHarnessRecord{}, err
	}
	if err := writeYAMLFile(profilePath, profile); err != nil {
		return metaHarnessRecord{}, err
	}
	if err := writeYAMLFile(launchPath, launch); err != nil {
		return metaHarnessRecord{}, err
	}
	a.reloadMetaHarnessRegistry()
	return metaHarnessRecordFromCatalog(root, id, profile, launch), nil
}

func applyMetaHarnessRequest(profile *bootprofile.Profile, launch *bootprofile.Launch, req metaHarnessRequest) {
	id := strings.TrimSpace(req.ID)
	profile.ID = id
	profile.Launch = id
	profile.DisplayName = strings.TrimSpace(req.DisplayName)
	if profile.DisplayName == "" {
		profile.DisplayName = id
	}
	profile.MCPServers = cleanStringSlice(req.MCPServers)
	profile.Identity.LineageAlias = firstNonEmpty(profile.Identity.LineageAlias, id)
	profile.Identity.Role = strings.TrimSpace(req.Role)
	profile.Identity.Project = strings.TrimSpace(req.Project)
	profile.Identity.WorkRoot = strings.TrimSpace(req.WorkRoot)
	profile.Identity.TrackingRoot = strings.TrimSpace(req.TrackingRoot)

	launch.ID = id
	launch.Provider = strings.TrimSpace(req.Provider)
	launch.Workdir = strings.TrimSpace(req.Workdir)
	launch.UILabel = strings.TrimSpace(req.UILabel)
	if launch.UILabel == "" {
		launch.UILabel = profile.DisplayName
	}
	launch.BootMode = strings.TrimSpace(req.BootMode)
	launch.Args = cleanStringSlice(req.Args)
	launch.Env = cleanStringMap(req.Env)
}

func defaultMetaHarnessProfile(id string) bootprofile.Profile {
	return bootprofile.Profile{
		ID:          id,
		DisplayName: id,
		Launch:      id,
		Identity: bootprofile.Identity{
			LineageAlias: id,
		},
		Slots: map[string]bootprofile.SlotSource{
			"agent": {
				Type:    "text",
				Content: "You are {{lineage_alias}}.\n",
			},
		},
	}
}

func metaHarnessRecordFromCatalog(root, id string, profile bootprofile.Profile, launch bootprofile.Launch) metaHarnessRecord {
	return metaHarnessRecord{
		ID:            id,
		DisplayName:   profile.DisplayName,
		Launch:        profile.Launch,
		UILabel:       launch.UILabel,
		Provider:      launch.Provider,
		ProviderAlias: launch.Provider,
		Workdir:       launch.Workdir,
		BootMode:      launch.BootMode,
		Args:          nonNilSlice(launch.Args),
		Env:           nonNilMap(launch.Env),
		Role:          profile.Identity.Role,
		Project:       profile.Identity.Project,
		WorkRoot:      profile.Identity.WorkRoot,
		TrackingRoot:  profile.Identity.TrackingRoot,
		MCPServers:    nonNilSlice(profile.MCPServers),
		ProfilePath:   metaHarnessProfilePath(root, id),
		LaunchPath:    metaHarnessLaunchPath(root, id),
	}
}

func (a *API) metaHarnessCatalogRoot() (string, error) {
	if a.Services != nil && a.Services.BootProfileCatalogPath != "" {
		return a.Services.BootProfileCatalogPath, nil
	}
	return "", fmt.Errorf("boot profile catalog is not configured")
}

func metaHarnessProfilePath(root, id string) string {
	return filepath.Join(root, "boot-profiles", id+".yaml")
}

func metaHarnessLaunchPath(root, id string) string {
	return filepath.Join(root, "launches", id+".yaml")
}

func validateMetaHarnessID(id string) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if !metaHarnessIDPattern.MatchString(id) {
		return fmt.Errorf("id may only contain letters, numbers, '.', '_' and '-'")
	}
	return nil
}

func writeYAMLFile(path string, value any) error {
	data, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (a *API) reloadMetaHarnessRegistry() {
	if a.Services != nil && a.Services.BootProfiles != nil {
		_ = a.Services.BootProfiles.Reload()
	}
}

func cleanStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func cleanStringMap(values map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func nonNilMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}
