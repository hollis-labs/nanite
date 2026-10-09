package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/store"
)

// ProfileRetirementReceipt identifies an owner-private export. Returning a
// prepared receipt does not mean that any profile has been retired.
type ProfileRetirementReceipt struct {
	ExportID         string    `json:"export_id"`
	ProfileID        string    `json:"profile_id"`
	Revision         string    `json:"revision"`
	Digest           string    `json:"digest"`
	ExportedAt       time.Time `json:"exported_at"`
	Retired          bool      `json:"retired"`
	ReceiptPersisted bool      `json:"receipt_persisted"`
}

type profileRetirementArchive struct {
	Receipt ProfileRetirementReceipt      `json:"receipt"`
	Export  store.ProfileRetirementExport `json:"export"`
}

// retirementArchiveRoot is host-derived, never supplied by an HTTP caller.
// Only private regular files are read; exports may contain credentials.
func (s *AgentConfigService) retirementArchiveRoot(ctx context.Context) (*os.Root, error) {
	parent, err := os.OpenRoot(filepath.Dir(s.store.DBPath(ctx)))
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	const dir = "profile-retirements"
	if mkdirErr := parent.Mkdir(dir, 0700); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
		return nil, mkdirErr
	}
	info, err := parent.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("profile export directory must be owner-private")
	}
	parentDir, err := parent.Open(".")
	if err != nil {
		return nil, err
	}
	if err := errors.Join(parentDir.Sync(), parentDir.Close()); err != nil {
		return nil, err
	}
	return parent.OpenRoot(dir)
}

func writeRetirementFile(root *os.Root, name string, value any) error {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err = json.NewEncoder(f).Encode(value); err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

// ExportEditableProfile persists and syncs the consistent private snapshot
// before publishing its receipt. No profile or grant is changed here.
func (s *AgentConfigService) ExportEditableProfile(ctx context.Context, id string) (ProfileRetirementReceipt, error) {
	snapshot, err := s.store.ExportProfileRetirement(ctx, id)
	if err != nil {
		return ProfileRetirementReceipt{}, err
	}
	digest, err := snapshot.Digest()
	if err != nil {
		return ProfileRetirementReceipt{}, err
	}
	receipt := ProfileRetirementReceipt{ExportID: uuid.NewString(), ProfileID: id, Revision: snapshot.Revision, Digest: digest, ExportedAt: time.Now().UTC(), ReceiptPersisted: true}
	root, err := s.retirementArchiveRoot(ctx)
	if err != nil {
		return ProfileRetirementReceipt{}, err
	}
	defer func() { _ = root.Close() }()
	err = writeRetirementFile(root, receipt.ExportID+".json", profileRetirementArchive{Receipt: receipt, Export: snapshot})
	if err != nil {
		return ProfileRetirementReceipt{}, err
	}
	return receipt, nil
}

// RetireEditableProfile requires the persisted export, then asks the store to
// compare its full state inside the deletion transaction. The archive survives
// both success and conflict. It cannot be used to recreate authority.
func (s *AgentConfigService) RetireEditableProfile(ctx context.Context, id, exportID, digest string) (ProfileRetirementReceipt, error) {
	parsed, err := uuid.Parse(exportID)
	if err != nil || parsed.String() != exportID {
		return ProfileRetirementReceipt{}, store.ErrProfileRetirementConflict
	}
	root, err := s.retirementArchiveRoot(ctx)
	if err != nil {
		return ProfileRetirementReceipt{}, err
	}
	defer func() { _ = root.Close() }()
	name := exportID + ".json"
	info, err := root.Lstat(name)
	if err != nil {
		return ProfileRetirementReceipt{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > store.ProfileExportMaxBytes+4096 {
		return ProfileRetirementReceipt{}, store.ErrProfileRetirementConflict
	}
	f, err := root.Open(name)
	if err != nil {
		return ProfileRetirementReceipt{}, err
	}
	var archive profileRetirementArchive
	decoder := json.NewDecoder(io.LimitReader(f, store.ProfileExportMaxBytes+4097))
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
		return ProfileRetirementReceipt{}, err
	}
	actual, err := archive.Export.Digest()
	receipt := archive.Receipt
	if err != nil || receipt.ExportID != exportID || receipt.ProfileID != id || archive.Export.ProfileID != id || receipt.Revision != archive.Export.Revision || digest == "" || digest != actual || receipt.Digest != actual || receipt.Retired {
		return ProfileRetirementReceipt{}, store.ErrProfileRetirementConflict
	}
	// A previously completed receipt supports an attributable retry without
	// replaying the deletion or touching any replacement profile.
	if info, statErr := root.Lstat(exportID + ".retired.json"); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4096 {
			return receipt, store.ErrProfileRetirementConflict
		}
		completed, openErr := root.Open(exportID + ".retired.json")
		if openErr != nil {
			return receipt, openErr
		}
		var result ProfileRetirementReceipt
		err = json.NewDecoder(io.LimitReader(completed, 4097)).Decode(&result)
		err = errors.Join(err, completed.Close())
		expected := receipt
		expected.Retired = true
		expected.ReceiptPersisted = true
		if err != nil || result != expected {
			return receipt, store.ErrProfileRetirementConflict
		}
		return result, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return receipt, statErr
	}
	if err := s.store.RetireExportedProfile(ctx, id, digest); err != nil {
		return receipt, err
	}
	receipt.Retired = true
	receipt.ReceiptPersisted = false
	s.emit(archive.Export.Slug, "deleted")
	// Preserve the committed outcome even if final receipt persistence fails.
	persisted := receipt
	persisted.ReceiptPersisted = true
	if err := writeRetirementFile(root, exportID+".retired.json", persisted); err != nil {
		return receipt, fmt.Errorf("profile retired; final receipt persistence failed: %w", err)
	}
	return persisted, nil
}
