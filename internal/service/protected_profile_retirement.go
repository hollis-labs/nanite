package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/substrate/mesh/agentdef"
)

// ProtectedRetirementRequest requires deliberate owner acknowledgement. The
// attribution fields do not authenticate the caller or grant any authority.
type ProtectedRetirementRequest struct {
	Acknowledge   bool   `json:"acknowledge_protected_retirement"`
	Actor         string `json:"actor"`
	Reason        string `json:"reason"`
	KeepProfileID string `json:"keep_profile_id"`
	KeepRevision  string `json:"keep_revision"`
}

type ProtectedRetirementReceipt struct {
	ProfileRetirementReceipt
	Authority store.ProtectedRetirementAuthority `json:"authority"`
}

type protectedRetirementArchive struct {
	Receipt ProtectedRetirementReceipt    `json:"receipt"`
	Export  store.ProfileRetirementExport `json:"export"`
}

// Bounds include worst-case JSON escaping of the bounded attribution fields.
const protectedRetirementMaxMetadata = 32 << 10

func (s *AgentConfigService) protectedRetirementAuthority(ctx context.Context, host *CognitiveViews, id string, req ProtectedRetirementRequest) (store.ProtectedRetirementAuthority, error) {
	authority := store.ProtectedRetirementAuthority{Actor: req.Actor, Reason: req.Reason, KeepProfileID: req.KeepProfileID, KeepRevision: req.KeepRevision}
	if !req.Acknowledge || req.KeepProfileID == "" || req.KeepProfileID == id {
		return authority, store.ErrProtectedRetirementAuthority
	}
	keep, err := s.store.GetAgent(ctx, req.KeepProfileID)
	if err != nil {
		return authority, err
	}
	if checkErr := authority.Validate(id); checkErr != nil {
		return authority, checkErr
	}
	if keep.Revision != req.KeepRevision {
		return authority, store.ErrProfileRetirementConflict
	}
	var settings struct {
		DefinitionRef DefinitionRef `json:"provisioned_definition_ref"`
	}
	if checkErr := json.Unmarshal([]byte(keep.Settings), &settings); checkErr != nil || settings.DefinitionRef.Validate() != nil || keep.PluginID != "" || keep.CanExecute || keep.Durable {
		return authority, store.ErrProtectedRetirementAuthority
	}
	generalChat, err := EmbeddedDefinition()
	if err != nil {
		return authority, err
	}
	// The host's approved general-chat artifact selects the keep class. Its
	// pin is derived from the artifact, never hardcoded from an operator window.
	if settings.DefinitionRef != generalChat.Ref {
		return authority, store.ErrProtectedRetirementAuthority
	}
	if host == nil || host.Resolver == nil {
		return authority, ErrUnsupportedDefinition
	}
	verified, err := host.Resolver.Resolve(ctx, settings.DefinitionRef)
	if err != nil {
		return authority, err
	}
	if verified.Ref != settings.DefinitionRef || verified.Definition == nil || verified.Definition.DefinitionID != settings.DefinitionRef.DefinitionID || verified.Definition.Revision != settings.DefinitionRef.Revision {
		return authority, ErrDefinitionDigestMismatch
	}
	digest, err := agentdef.Digest(verified.Definition)
	if err != nil || digest != settings.DefinitionRef.SemanticDigest {
		return authority, ErrDefinitionDigestMismatch
	}
	cfg, err := MapChatDefinition(verified)
	if err != nil {
		return authority, err
	}
	if cfg.PermissionProfile != "default" {
		return authority, ErrUnsupportedDefinition
	}
	return authority, nil
}

func (s *AgentConfigService) ExportProtectedProfile(ctx context.Context, host *CognitiveViews, id string, req ProtectedRetirementRequest) (ProtectedRetirementReceipt, error) {
	authority, err := s.protectedRetirementAuthority(ctx, host, id, req)
	if err != nil {
		return ProtectedRetirementReceipt{}, err
	}
	snapshot, err := s.store.ExportProtectedProfileRetirement(ctx, id, authority)
	if err != nil {
		return ProtectedRetirementReceipt{}, err
	}
	digest, err := snapshot.Digest()
	if err != nil {
		return ProtectedRetirementReceipt{}, err
	}
	receipt := ProtectedRetirementReceipt{ProfileRetirementReceipt: ProfileRetirementReceipt{ExportID: uuid.NewString(), ProfileID: id, Revision: snapshot.Revision, Digest: digest, ExportedAt: time.Now().UTC(), ReceiptPersisted: true}, Authority: authority}
	root, err := s.retirementArchiveRoot(ctx)
	if err != nil {
		return ProtectedRetirementReceipt{}, err
	}
	defer func() { _ = root.Close() }()
	if checkErr := writeRetirementFile(root, receipt.ExportID+".protected.json", protectedRetirementArchive{Receipt: receipt, Export: snapshot}); checkErr != nil {
		return ProtectedRetirementReceipt{}, checkErr
	}
	return receipt, nil
}

// RetireProtectedProfile uses only the owner-private persisted export and its
// retained identity. A failed final receipt write preserves the committed result;
// a retry reconciles it from the transactionally persisted suppression audit.
func (s *AgentConfigService) RetireProtectedProfile(ctx context.Context, id, exportID, digest string, req ProtectedRetirementRequest) (ProtectedRetirementReceipt, error) {
	parsed, err := uuid.Parse(exportID)
	if !req.Acknowledge || err != nil || parsed.String() != exportID {
		return ProtectedRetirementReceipt{}, store.ErrProtectedRetirementAuthority
	}
	root, err := s.retirementArchiveRoot(ctx)
	if err != nil {
		return ProtectedRetirementReceipt{}, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(exportID + ".protected.json")
	if err != nil {
		return ProtectedRetirementReceipt{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > store.ProfileExportMaxBytes+protectedRetirementMaxMetadata {
		return ProtectedRetirementReceipt{}, store.ErrProfileRetirementConflict
	}
	f, err := root.Open(exportID + ".protected.json")
	if err != nil {
		return ProtectedRetirementReceipt{}, err
	}
	var archive protectedRetirementArchive
	decoder := json.NewDecoder(io.LimitReader(f, store.ProfileExportMaxBytes+protectedRetirementMaxMetadata+1))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&archive)
	if err == nil {
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			err = store.ErrProfileRetirementConflict
		}
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return ProtectedRetirementReceipt{}, err
	}
	r := archive.Receipt
	a := r.Authority
	actual, err := archive.Export.Digest()
	if err != nil || archive.Export.SchemaVersion != 2 || r.ExportID != exportID || r.ProfileID != id || archive.Export.ProfileID != id || r.Revision != archive.Export.Revision || digest == "" || digest != actual || r.Digest != actual || r.Retired || !r.ReceiptPersisted || a.Validate(id) != nil || a.Actor != req.Actor || a.Reason != req.Reason || a.KeepProfileID != req.KeepProfileID || a.KeepRevision != req.KeepRevision {
		return ProtectedRetirementReceipt{}, store.ErrProfileRetirementConflict
	}
	committed, err := s.store.ProtectedProfileRetirementCommitted(ctx, id, exportID, digest, a)
	if err != nil {
		return r, err
	}
	if !committed {
		// A final receipt cannot predate the transaction's durable audit. Refuse
		// inconsistent archive state before any deletion effect.
		if _, statErr := root.Lstat(exportID + ".protected.retired.json"); !errors.Is(statErr, os.ErrNotExist) {
			return r, store.ErrProfileRetirementConflict
		}
		if checkErr := s.store.RetireExportedProtectedProfile(ctx, id, exportID, digest, a); checkErr != nil {
			return r, checkErr
		}
		s.emit(archive.Export.Slug, "deleted")
	}
	r.Retired = true
	r.ReceiptPersisted = false
	completed := r
	completed.ReceiptPersisted = true
	name := exportID + ".protected.retired.json"
	if info, statErr := root.Lstat(name); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > protectedRetirementMaxMetadata {
			return r, store.ErrProfileRetirementConflict
		}
		f, openErr := root.Open(name)
		if openErr != nil {
			return r, openErr
		}
		var result ProtectedRetirementReceipt
		decoder := json.NewDecoder(io.LimitReader(f, protectedRetirementMaxMetadata+1))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&result)
		if err == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				err = store.ErrProfileRetirementConflict
			}
		}
		err = errors.Join(err, f.Close())
		if err != nil || result != completed {
			return r, store.ErrProfileRetirementConflict
		}
		return result, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return r, statErr
	}
	if checkErr := writeRetirementFile(root, name, completed); checkErr != nil {
		return r, fmt.Errorf("profile retired; final receipt persistence failed: %w", checkErr)
	}
	return completed, nil
}
