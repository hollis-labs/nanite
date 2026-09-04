package workflowhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hollis-labs/go-workflow/values"
)

const (
	// ArtifactStoreName and ArtifactStoreVersion are the exact immutable
	// identity recorded in Nanite's workflow host contract.
	ArtifactStoreName    = "nanite-content-addressed-artifacts"
	ArtifactStoreVersion = "v1"

	workflowArtifactDirectory = "workflow-cas"
	artifactScheme            = "artifact"
	artifactPayloadName       = "payload"
	artifactManifestName      = "manifest.json"
)

// WorkflowArtifactRoot namespaces go-workflow content below Nanite's configured
// artifact root without taking ownership of existing session-artifact files.
func WorkflowArtifactRoot(configuredRoot string) string {
	return filepath.Join(configuredRoot, workflowArtifactDirectory)
}

var artifactComponentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._~-]*$`)

type artifactLocator struct {
	scope      values.ArtifactOwnerScope
	ownerHash  string
	artifactID string
}

func validateArtifactRef(ref values.ArtifactRef) error {
	if err := ref.Validate(); err != nil || ref.Store != ArtifactStoreName {
		return values.ErrArtifactInvalid
	}
	parsed, err := url.Parse(ref.URI)
	if err != nil || parsed.Scheme != artifactScheme || parsed.Host != ArtifactStoreName ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" ||
		parsed.RawPath != "" || parsed.Path == "" || parsed.Path[0] != '/' {
		return values.ErrArtifactInvalid
	}
	segments := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(segments) != 3 || len(segments[1]) != sha256.Size*2 || len(segments[2]) != sha256.Size*2 {
		return values.ErrArtifactInvalid
	}
	for _, segment := range segments {
		if !artifactComponentPattern.MatchString(segment) || segment == "." || segment == ".." {
			return values.ErrArtifactInvalid
		}
	}
	if !values.ArtifactOwnerScope(segments[0]).Valid() || !artifactLowerHex(segments[1]) ||
		!artifactLowerHex(segments[2]) || parsed.String() != ref.URI {
		return values.ErrArtifactInvalid
	}
	return nil
}

func parseArtifactRef(ref values.ArtifactRef) (artifactLocator, error) {
	if err := validateArtifactRef(ref); err != nil {
		return artifactLocator{}, err
	}
	parsed, _ := url.Parse(ref.URI)
	segments := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	return artifactLocator{
		scope:      values.ArtifactOwnerScope(segments[0]),
		ownerHash:  segments[1],
		artifactID: segments[2],
	}, nil
}

func artifactURI(locator artifactLocator) string {
	return artifactScheme + "://" + ArtifactStoreName + "/" + string(locator.scope) + "/" + locator.ownerHash + "/" + locator.artifactID
}

func artifactOwnerHash(ownerID string) string {
	digest := sha256.Sum256([]byte(ownerID))
	return hex.EncodeToString(digest[:])
}

func artifactLowerHex(value string) bool {
	if strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func rejectArtifactSymlinkComponents(path string) error {
	cleaned := filepath.Clean(path)
	volume := filepath.VolumeName(cleaned)
	remainder := strings.TrimPrefix(cleaned, volume)
	current := volume + string(os.PathSeparator)
	for _, component := range strings.Split(strings.TrimPrefix(remainder, string(os.PathSeparator)), string(os.PathSeparator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return values.ErrArtifactInvalid
		}
	}
	return nil
}

func ensureArtifactDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if mkdirErr := os.Mkdir(path, 0o700); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
			return mkdirErr
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return values.ErrArtifactInvalid
	}
	return nil
}

func ensureArtifactDirectoryChain(root string, components ...string) (string, error) {
	current := root
	for _, component := range components {
		if !artifactComponentPattern.MatchString(component) {
			return "", values.ErrArtifactInvalid
		}
		current = filepath.Join(current, component)
		if err := ensureArtifactDirectory(current); err != nil {
			return "", err
		}
	}
	return current, nil
}

func requireArtifactRegularFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, values.ErrArtifactInvalid
	}
	return info, nil
}

func openArtifactRegularFile(path string, maximumSize int64) (*os.File, os.FileInfo, error) {
	before, err := requireArtifactRegularFile(path)
	if err != nil {
		return nil, nil, err
	}
	if maximumSize >= 0 && before.Size() > maximumSize {
		return nil, nil, values.ErrArtifactSizeLimit
	}
	file, err := os.Open(path) // #nosec G304 -- callers derive paths beneath the identity-checked root.
	if err != nil {
		return nil, nil, err
	}
	after, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) || (maximumSize >= 0 && after.Size() > maximumSize) {
		_ = file.Close()
		return nil, nil, values.ErrArtifactInvalid
	}
	return file, after, nil
}

type artifactContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader artifactContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := reader.reader.Read(buffer)
	if contextErr := reader.ctx.Err(); contextErr != nil {
		return 0, contextErr
	}
	return n, err
}

func syncArtifactDirectory(path string) error {
	directory, err := os.Open(path) // #nosec G304 -- path is derived beneath the identity-checked root.
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
