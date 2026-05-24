package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hollis-labs/go-agent-launch/agentlaunch"
	"github.com/hollis-labs/nanite/internal/store"
)

const (
	agentBootPlanMaxTimeoutSeconds = 600
)

var (
	agentBootPlanSupportedVersions = map[int]bool{
		store.AgentBootPlanSchemaVersion1: true,
	}
	agentBootPlanSourceKinds = map[string]string{
		"path_file":    "file",
		"path_dir":     "directory",
		"literal_file": "file",
		"literal_dir":  "directory",
		"generated":    "",
	}
	agentBootPlanPlantTimings = map[string]int{
		"create":           1,
		"start":            2,
		"resume":           3,
		"every_boot":       4,
		"recovery_replant": 5,
	}
	agentBootPlanOverwritePolicies = map[string]bool{
		"never":           true,
		"if_missing":      true,
		"always":          true,
		"if_hash_differs": true,
	}
	agentBootPlanPlantFailurePolicies = map[string]bool{
		"fail_boot": true,
		"warn":      true,
		"skip":      true,
	}
	agentBootPlanCallbackTimings = map[string]int{
		"before_boot":       1,
		"after_boot":        2,
		"before_first_turn": 3,
		"on_resume":         4,
		"on_recovery":       5,
	}
	agentBootPlanCallbackTypes = map[string]bool{
		"command":           true,
		"tool_call":         true,
		"message_injection": true,
		"http_request":      true,
		"local_api":         true,
	}
	agentBootPlanCallbackFailurePolicies = map[string]bool{
		"fail_boot":  true,
		"warn":       true,
		"retry_once": true,
		"ignore":     true,
	}
	agentBootPlanExecutableCallbackTypes = map[string]bool{}
)

func (a *API) handleGetAgentBootPlan(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	doc, err := a.Services.Store.GetAgentBootPlan(r.Context(), agent.ID)
	if err != nil {
		if errors.Is(err, store.ErrAgentBootPlanNotFound) {
			a.jsonResp(w, http.StatusOK, store.EmptyAgentBootPlan(agent.ID))
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, doc)
}

func (a *API) handlePutAgentBootPlan(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	var doc store.AgentBootPlanDocument
	if err := a.decode(r, &doc); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if doc.AgentID != "" && doc.AgentID != agent.ID {
		a.errorResp(w, http.StatusBadRequest, "agent_id in body must match path")
		return
	}
	preview := dryRunAgentBootPlan(agent.ID, &doc)
	if !preview.Valid {
		a.jsonResp(w, http.StatusBadRequest, preview)
		return
	}
	saved, err := a.Services.Store.PutAgentBootPlan(r.Context(), preview.NormalizedPlan)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, saved)
}

func (a *API) handleDeleteAgentBootPlan(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireMutableAgent(w, r)
	if !ok {
		return
	}
	if err := a.Services.Store.DeleteAgentBootPlan(r.Context(), agent.ID); err != nil {
		if errors.Is(err, store.ErrAgentBootPlanNotFound) {
			a.errorResp(w, http.StatusNotFound, "boot plan not found")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *API) handleDryRunAgentBootPlan(w http.ResponseWriter, r *http.Request) {
	agent, ok := a.requireAgent(w, r)
	if !ok {
		return
	}
	doc, explicitBody, err := a.decodeOptionalBootPlan(r)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if !explicitBody || doc == nil {
		stored, err := a.Services.Store.GetAgentBootPlan(r.Context(), agent.ID)
		if err != nil {
			if errors.Is(err, store.ErrAgentBootPlanNotFound) {
				doc = store.EmptyAgentBootPlan(agent.ID)
			} else {
				a.errorResp(w, http.StatusInternalServerError, err.Error())
				return
			}
		} else {
			doc = stored
		}
	} else if doc.AgentID != "" && doc.AgentID != agent.ID {
		a.errorResp(w, http.StatusBadRequest, "agent_id in body must match path")
		return
	}
	preview := dryRunAgentBootPlan(agent.ID, doc)
	a.jsonResp(w, http.StatusOK, preview)
}

func (a *API) decodeOptionalBootPlan(r *http.Request) (*store.AgentBootPlanDocument, bool, error) {
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, false, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, false, nil
	}
	var req AgentBootPlanDryRunRequest
	if err := json.Unmarshal(body, &req); err == nil && req.Plan != nil {
		return req.Plan, true, nil
	}
	var doc store.AgentBootPlanDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, false, err
	}
	return &doc, true, nil
}

func dryRunAgentBootPlan(agentID string, in *store.AgentBootPlanDocument) AgentBootPlanDryRunResponse {
	doc := store.EmptyAgentBootPlan(agentID)
	if in != nil {
		*doc = *in
	}
	doc.AgentID = agentID
	if doc.SchemaVersion == 0 {
		doc.SchemaVersion = store.AgentBootPlanSchemaVersion1
	}
	if doc.PlantItems == nil {
		doc.PlantItems = []store.AgentBootPlantItem{}
	}
	if doc.Callbacks == nil {
		doc.Callbacks = []store.AgentBootCallback{}
	}

	errorsList := make([]string, 0)
	warnings := make([]string, 0)
	unsupported := make([]string, 0)

	if !agentBootPlanSupportedVersions[doc.SchemaVersion] {
		errorsList = append(errorsList, fmt.Sprintf("unsupported schema_version %d", doc.SchemaVersion))
	}

	plantIDs := map[string]bool{}
	normalizedItems := make([]store.AgentBootPlantItem, 0, len(doc.PlantItems))
	for i, item := range doc.PlantItems {
		prefix := fmt.Sprintf("plant_items[%d]", i)
		if strings.TrimSpace(item.ID) == "" {
			errorsList = append(errorsList, prefix+".id is required")
		} else if plantIDs[item.ID] {
			errorsList = append(errorsList, prefix+".id must be unique")
		}
		plantIDs[item.ID] = true
		item.ID = strings.TrimSpace(item.ID)
		item.Name = strings.TrimSpace(item.Name)
		item.SourceKind = strings.TrimSpace(item.SourceKind)
		item.SourcePath = strings.TrimSpace(item.SourcePath)
		item.TargetRelPath = strings.TrimSpace(filepath.ToSlash(item.TargetRelPath))
		item.EntryKind = strings.TrimSpace(item.EntryKind)
		item.OverwritePolicy = strings.TrimSpace(item.OverwritePolicy)
		item.FailurePolicy = strings.TrimSpace(item.FailurePolicy)

		expectedEntryKind, ok := agentBootPlanSourceKinds[item.SourceKind]
		if !ok {
			errorsList = append(errorsList, prefix+".source_kind is invalid")
		}
		if item.EntryKind != "file" && item.EntryKind != "directory" {
			errorsList = append(errorsList, prefix+".entry_kind must be file or directory")
		}
		if expectedEntryKind != "" && item.EntryKind != "" && item.EntryKind != expectedEntryKind {
			errorsList = append(errorsList, prefix+".entry_kind does not match source_kind")
		}
		if err := validateBootPlanRelPath(item.TargetRelPath); err != nil {
			errorsList = append(errorsList, prefix+".target_rel_path "+err.Error())
		}
		if strings.HasPrefix(item.SourceKind, "path_") && item.SourcePath == "" {
			errorsList = append(errorsList, prefix+".source_path is required for path-backed items")
		}
		if strings.HasPrefix(item.SourceKind, "literal_") && item.Content == "" {
			errorsList = append(errorsList, prefix+".content is required for literal items")
		}
		if item.SourceKind == "generated" && item.Generator == nil && item.Content == "" {
			errorsList = append(errorsList, prefix+".generator or content is required for generated items")
		}
		if item.Generator != nil {
			item.Generator.Kind = strings.TrimSpace(item.Generator.Kind)
			if item.Generator.Kind == "" {
				errorsList = append(errorsList, prefix+".generator.kind is required when generator is present")
			}
		}
		if !agentBootPlanOverwritePolicies[item.OverwritePolicy] {
			errorsList = append(errorsList, prefix+".overwrite_policy is invalid")
		}
		if !agentBootPlanPlantFailurePolicies[item.FailurePolicy] {
			errorsList = append(errorsList, prefix+".failure_policy is invalid")
		}
		timingSet := map[string]bool{}
		normalizedTiming := make([]string, 0, len(item.Timing))
		for _, timing := range item.Timing {
			timing = strings.TrimSpace(timing)
			if timing == "" {
				continue
			}
			if _, ok := agentBootPlanPlantTimings[timing]; !ok {
				errorsList = append(errorsList, prefix+".timing contains invalid value "+timing)
				continue
			}
			if timingSet[timing] {
				errorsList = append(errorsList, prefix+".timing contains duplicate value "+timing)
				continue
			}
			timingSet[timing] = true
			normalizedTiming = append(normalizedTiming, timing)
		}
		if len(normalizedTiming) == 0 {
			errorsList = append(errorsList, prefix+".timing must contain at least one value")
		}
		sort.Slice(normalizedTiming, func(i, j int) bool {
			return agentBootPlanPlantTimings[normalizedTiming[i]] < agentBootPlanPlantTimings[normalizedTiming[j]]
		})
		item.Timing = normalizedTiming
		normalizedItems = append(normalizedItems, item)
	}
	doc.PlantItems = normalizedItems

	callbackIDs := map[string]bool{}
	normalizedCallbacks := make([]store.AgentBootCallback, 0, len(doc.Callbacks))
	for i, cb := range doc.Callbacks {
		prefix := fmt.Sprintf("callbacks[%d]", i)
		cb.ID = strings.TrimSpace(cb.ID)
		cb.Name = strings.TrimSpace(cb.Name)
		cb.Timing = strings.TrimSpace(cb.Timing)
		cb.CallbackType = strings.TrimSpace(cb.CallbackType)
		cb.ToolName = strings.TrimSpace(cb.ToolName)
		cb.Message = strings.TrimSpace(cb.Message)
		if cb.Request != nil {
			cb.Request.Method = strings.TrimSpace(cb.Request.Method)
			cb.Request.URL = strings.TrimSpace(cb.Request.URL)
			cb.Request.Path = strings.TrimSpace(cb.Request.Path)
		}
		if strings.TrimSpace(cb.ID) == "" {
			errorsList = append(errorsList, prefix+".id is required")
		} else if callbackIDs[cb.ID] {
			errorsList = append(errorsList, prefix+".id must be unique")
		}
		callbackIDs[cb.ID] = true
		if _, ok := agentBootPlanCallbackTimings[cb.Timing]; !ok {
			errorsList = append(errorsList, prefix+".timing is invalid")
		}
		if !agentBootPlanCallbackTypes[cb.CallbackType] {
			errorsList = append(errorsList, prefix+".callback_type is invalid")
		}
		if cb.TimeoutSeconds <= 0 || cb.TimeoutSeconds > agentBootPlanMaxTimeoutSeconds {
			errorsList = append(errorsList, fmt.Sprintf("%s.timeout_seconds must be between 1 and %d", prefix, agentBootPlanMaxTimeoutSeconds))
		}
		if !agentBootPlanCallbackFailurePolicies[cb.FailurePolicy] {
			errorsList = append(errorsList, prefix+".failure_policy is invalid")
		}
		switch cb.CallbackType {
		case "command":
			if cb.Command == nil || len(cb.Command.Argv) == 0 {
				errorsList = append(errorsList, prefix+".command.argv is required for command callbacks")
			}
		case "tool_call":
			if cb.ToolName == "" {
				errorsList = append(errorsList, prefix+".tool_name is required for tool_call callbacks")
			}
		case "message_injection":
			if cb.Message == "" {
				errorsList = append(errorsList, prefix+".message is required for message_injection callbacks")
			}
		case "http_request", "local_api":
			if cb.Request == nil {
				errorsList = append(errorsList, prefix+".request is required for request callbacks")
			}
		}
		if cb.Enabled && !agentBootPlanExecutableCallbackTypes[cb.CallbackType] {
			unsupported = append(unsupported, fmt.Sprintf("callback %q is stored but not executed yet (%s)", firstNonEmpty(cb.Name, cb.ID), cb.CallbackType))
		}
		normalizedCallbacks = append(normalizedCallbacks, cb)
	}
	doc.Callbacks = normalizedCallbacks

	plantOps := make([]AgentBootPlanPlantOperation, 0, len(doc.PlantItems))
	for _, item := range doc.PlantItems {
		op := AgentBootPlanPlantOperation{
			ItemID:          item.ID,
			Name:            item.Name,
			Timing:          append([]string(nil), item.Timing...),
			TargetRelPath:   item.TargetRelPath,
			EntryKind:       item.EntryKind,
			SourceKind:      item.SourceKind,
			OverwritePolicy: item.OverwritePolicy,
			FailurePolicy:   item.FailurePolicy,
			Enabled:         item.Enabled,
			Secret:          item.Secret,
		}
		if item.Secret {
			if item.SourcePath != "" {
				op.SourcePathRedacted = true
			}
			if item.Content != "" {
				op.ContentPreviewRedacted = true
			}
			op.Notes = append(op.Notes, "secret item details are redacted in dry-run")
		} else {
			op.SourcePath = item.SourcePath
			if item.Content != "" {
				op.ContentPreview = item.Content
			}
		}
		if item.SourceKind == "generated" && item.Generator != nil {
			op.Notes = append(op.Notes, "generated content preview is declarative only in this phase")
		}
		plantOps = append(plantOps, op)
	}
	sort.Slice(plantOps, func(i, j int) bool {
		left := minPlantTimingRank(plantOps[i].Timing)
		right := minPlantTimingRank(plantOps[j].Timing)
		if left != right {
			return left < right
		}
		if plantOps[i].TargetRelPath != plantOps[j].TargetRelPath {
			return plantOps[i].TargetRelPath < plantOps[j].TargetRelPath
		}
		return plantOps[i].ItemID < plantOps[j].ItemID
	})

	callbackOps := make([]AgentBootPlanCallbackOperation, 0, len(doc.Callbacks))
	for _, cb := range doc.Callbacks {
		op := AgentBootPlanCallbackOperation{
			CallbackID:     cb.ID,
			Name:           cb.Name,
			Timing:         cb.Timing,
			CallbackType:   cb.CallbackType,
			TimeoutSeconds: cb.TimeoutSeconds,
			FailurePolicy:  cb.FailurePolicy,
			Enabled:        cb.Enabled,
		}
		switch cb.CallbackType {
		case "command":
			if cb.Command != nil {
				op.PayloadPreview = strings.Join(cb.Command.Argv, " ")
			}
		case "tool_call":
			op.PayloadPreview = cb.ToolName
		case "message_injection":
			op.PayloadPreview = cb.Message
		case "http_request", "local_api":
			if cb.Request != nil {
				op.PayloadPreview = firstNonEmpty(cb.Request.Method, "REQUEST") + " " + firstNonEmpty(cb.Request.URL, cb.Request.Path)
			}
		}
		if len(cb.Env) > 0 {
			op.EnvRedacted = true
			op.Notes = append(op.Notes, "environment values are redacted in dry-run")
		}
		if cb.Enabled && !agentBootPlanExecutableCallbackTypes[cb.CallbackType] {
			op.Notes = append(op.Notes, "configured but not executed by this phase")
		}
		callbackOps = append(callbackOps, op)
	}
	sort.Slice(callbackOps, func(i, j int) bool {
		left := agentBootPlanCallbackTimings[callbackOps[i].Timing]
		right := agentBootPlanCallbackTimings[callbackOps[j].Timing]
		if left != right {
			return left < right
		}
		return callbackOps[i].CallbackID < callbackOps[j].CallbackID
	})

	sort.Strings(errorsList)
	sort.Strings(warnings)
	sort.Strings(unsupported)
	doc.CreatedAt = ""
	doc.UpdatedAt = ""

	return AgentBootPlanDryRunResponse{
		Valid:            len(errorsList) == 0,
		Errors:           errorsList,
		Warnings:         warnings,
		NormalizedPlan:   *doc,
		PlantOperations:  plantOps,
		CallbackOrder:    callbackOps,
		UnsupportedNotes: unsupported,
	}
}

func validateBootPlanRelPath(rel string) error {
	if rel == "" {
		return errors.New("must not be empty")
	}
	if strings.HasPrefix(rel, "~") {
		return errors.New("must not use home expansion")
	}
	if filepath.IsAbs(rel) {
		return errors.New("must be relative")
	}
	if err := agentlaunch.ValidateBootDirRelPath(rel); err != nil {
		return err
	}
	return nil
}

func minPlantTimingRank(timings []string) int {
	best := 999
	for _, timing := range timings {
		if rank, ok := agentBootPlanPlantTimings[timing]; ok && rank < best {
			best = rank
		}
	}
	return best
}
