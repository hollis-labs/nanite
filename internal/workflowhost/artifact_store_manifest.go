package workflowhost

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/hollis-labs/go-workflow/values"
)

const artifactManifestVersion = 1

type artifactManifest struct {
	Version  int                     `json:"version"`
	Metadata values.ArtifactMetadata `json:"metadata"`
}

type artifactIdentity struct {
	Store      string                    `json:"store"`
	OwnerScope values.ArtifactOwnerScope `json:"owner_scope"`
	OwnerHash  string                    `json:"owner_hash"`
	Digest     string                    `json:"digest"`
	MediaType  string                    `json:"media_type"`
	SizeBytes  int64                     `json:"size_bytes"`
	Producer   values.Producer           `json:"producer"`
	Redaction  values.RedactionClass     `json:"redaction"`
	Retention  values.RetentionClass     `json:"retention"`
	CreatedAt  time.Time                 `json:"created_at"`
	ExpiresAt  time.Time                 `json:"expires_at,omitempty"`
}

func computeArtifactID(identity artifactIdentity) (string, error) {
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func encodeArtifactManifest(metadata values.ArtifactMetadata) ([]byte, error) {
	if err := metadata.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(artifactManifest{Version: artifactManifestVersion, Metadata: metadata})
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func decodeArtifactManifest(content []byte) (artifactManifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var manifest artifactManifest
	if err := decoder.Decode(&manifest); err != nil {
		return artifactManifest{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return artifactManifest{}, values.ErrArtifactInvalid
		}
		return artifactManifest{}, err
	}
	if manifest.Version != artifactManifestVersion {
		return artifactManifest{}, values.ErrArtifactInvalid
	}
	if err := manifest.Metadata.Validate(); err != nil {
		return artifactManifest{}, err
	}
	return manifest, nil
}

func readArtifactManifest(directory string) (artifactManifest, error) {
	file, info, err := openArtifactRegularFile(filepath.Join(directory, artifactManifestName), 1<<20)
	if err != nil {
		return artifactManifest{}, err
	}
	content, readErr := io.ReadAll(io.LimitReader(file, 1<<20+1))
	closeErr := file.Close()
	if readErr != nil {
		return artifactManifest{}, readErr
	}
	if closeErr != nil {
		return artifactManifest{}, closeErr
	}
	if int64(len(content)) != info.Size() {
		return artifactManifest{}, values.ErrArtifactInvalid
	}
	return decodeArtifactManifest(content)
}

func verifyStoredArtifact(directory string, locator artifactLocator, expected *values.ArtifactRef) (values.ArtifactMetadata, error) {
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		return values.ArtifactMetadata{}, err
	}
	if !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
		return values.ArtifactMetadata{}, values.ErrArtifactInvalid
	}
	manifest, err := readArtifactManifest(directory)
	if err != nil {
		return values.ArtifactMetadata{}, err
	}
	metadata := manifest.Metadata
	if metadata.Ref.Store != ArtifactStoreName || metadata.Ref.URI != artifactURI(locator) ||
		metadata.Owner.Scope != locator.scope || artifactOwnerHash(metadata.Owner.ID) != locator.ownerHash {
		return values.ArtifactMetadata{}, values.ErrArtifactInvalid
	}
	identity := artifactIdentity{
		Store: ArtifactStoreName, OwnerScope: metadata.Owner.Scope, OwnerHash: locator.ownerHash,
		Digest: metadata.Ref.Digest, MediaType: metadata.Ref.MediaType, SizeBytes: metadata.Ref.SizeBytes,
		Producer: metadata.Ref.Producer, Redaction: metadata.Ref.Redaction, Retention: metadata.Ref.Retention,
		CreatedAt: metadata.CreatedAt, ExpiresAt: metadata.ExpiresAt,
	}
	computedID, err := computeArtifactID(identity)
	if err != nil || computedID != locator.artifactID {
		return values.ArtifactMetadata{}, values.ErrArtifactInvalid
	}
	if expected != nil && !reflect.DeepEqual(metadata.Ref, *expected) {
		return values.ArtifactMetadata{}, values.ErrArtifactInvalid
	}
	payloadInfo, err := requireArtifactRegularFile(filepath.Join(directory, artifactPayloadName))
	if err != nil {
		return values.ArtifactMetadata{}, err
	}
	if payloadInfo.Size() != metadata.Ref.SizeBytes {
		return values.ArtifactMetadata{}, values.ErrArtifactDigest
	}
	return metadata, nil
}
