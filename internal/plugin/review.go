package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hollis-labs/go-safefs/atomicfile"
	"github.com/hollis-labs/nanite/internal/secrets"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/plugin-sdk/manifest"
	sdkprocess "github.com/hollis-labs/plugin-sdk/subprocess"
)

const MaxBundleBytes int64 = 512 << 20
const MaxBundleFiles = 10000

// InstallReview describes actual staged bytes. It never contains resolved values.
type InstallReview struct {
	ID           string                         `json:"id"`
	Name         string                         `json:"name"`
	Version      string                         `json:"version"`
	BundleDigest string                         `json:"bundle_digest"`
	Arguments    []string                       `json:"arguments"`
	Entrypoint   string                         `json:"entrypoint"`
	Capabilities []sdkprocess.CapabilityRequest `json:"capabilities"`
	Secrets      []ReviewSecret                 `json:"secrets"`
	Environment  []string                       `json:"environment"`
	Tools        []ReviewTool                   `json:"tools"`
}

type ReviewSecret struct {
	Name        string `json:"name"`
	Environment string `json:"environment,omitempty"`
	Required    bool   `json:"required"`
}
type ReviewTool struct {
	Name   string `json:"name"`
	Effect string `json:"effect"`
}

type InstallApproval struct {
	SchemaVersion int           `json:"schema_version"`
	Review        InstallReview `json:"review"`
	ReviewDigest  string        `json:"review_digest"`
}

// Capability vocabulary belongs to the host, not to the shared SDK.
var capabilityEnvironment = map[string][]string{
	"ssh_agent":     {"SSH_AUTH_SOCK"},
	"docker_socket": {"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG"},
}

func BuildInstallReview(ctx context.Context, directory string) (InstallReview, error) {
	declared, err := ParseManifest(filepath.Join(directory, "plugin.yaml"))
	if err != nil {
		return InstallReview{}, err
	}
	if declared.Shared == nil {
		return InstallReview{}, fmt.Errorf("install review requires shared manifest")
	}
	digest, err := BundleDigest(ctx, directory)
	if err != nil {
		return InstallReview{}, err
	}
	common := declared.Shared
	review := InstallReview{ID: common.ID, Name: common.Name, Version: common.Version, Entrypoint: common.Entrypoint.Command, Arguments: append([]string{}, common.Entrypoint.Args...), BundleDigest: digest, Capabilities: []sdkprocess.CapabilityRequest{}, Secrets: []ReviewSecret{}, Environment: []string{}, Tools: []ReviewTool{}}
	for _, request := range common.Capabilities {
		if _, known := capabilityEnvironment[request.Name]; !known && !request.Optional {
			return InstallReview{}, fmt.Errorf("unknown required capability %q", request.Name)
		}
		review.Capabilities = append(review.Capabilities, request)
	}
	for name, secret := range common.Config.Secrets {
		review.Secrets = append(review.Secrets, ReviewSecret{Name: name, Environment: secret.Env, Required: secret.Required})
	}
	for _, field := range common.Config.Fields {
		if field.Env != "" {
			review.Environment = append(review.Environment, field.Env)
		}
	}
	for _, tool := range common.Tools {
		review.Tools = append(review.Tools, ReviewTool{Name: tool.Name, Effect: tool.Effect})
	}
	slices.SortFunc(review.Secrets, func(a, b ReviewSecret) int { return strings.Compare(a.Name, b.Name) })
	slices.Sort(review.Environment)
	return review, nil
}

func (review InstallReview) Digest() string {
	raw, _ := json.Marshal(review)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// BundleDigest covers every file's relative path, permissions and bytes.
// Symlinks, devices, oversized trees and cancellation refuse the bundle.
func BundleDigest(ctx context.Context, directory string) (string, error) {
	root, rootErr := os.OpenRoot(directory)
	if rootErr != nil {
		return "", rootErr
	}
	defer func() { _ = root.Close() }()
	hash := sha256.New()
	var total int64
	count := 0
	walkResult := fs.WalkDir(root.FS(), ".", func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("bundle contains nonregular file %q", file)
		}
		count++
		if count > MaxBundleFiles || info.Size() > MaxBundleBytes-total {
			return fmt.Errorf("bundle exceeds file or byte limit")
		}
		relative := file
		stream, err := root.Open(file)
		if err != nil {
			return err
		}
		opened, statErr := stream.Stat()
		if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
			_ = stream.Close()
			return fmt.Errorf("bundle file changed while opening %q", file)
		}
		contentHash := sha256.New()
		bytesRead, readErr := io.Copy(contentHash, io.LimitReader(&reviewReader{ctx: ctx, reader: stream}, MaxBundleBytes-total+1))
		closeErr := stream.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		total += bytesRead
		if total > MaxBundleBytes {
			return fmt.Errorf("bundle exceeds byte limit")
		}
		_, err = fmt.Fprintf(hash, "%s\x00%o\x00%d\x00%x\n", filepath.ToSlash(relative), opened.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky), bytesRead, contentHash.Sum(nil))
		return err
	})
	if walkResult != nil {
		return "", walkResult
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type reviewReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *reviewReader) Read(bytes []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(bytes)
}

// ApprovalPath is outside both the plugin bundle and the plugin's data directory.
func ApprovalPath(pluginsRoot, id string) (string, error) {
	if err := ValidatePluginID(id); err != nil {
		return "", err
	}
	return filepath.Join(pluginsRoot, ".approvals", id+".json"), nil
}

func ReadInstallApproval(pluginsRoot, id string) (*InstallApproval, error) {
	path, err := ApprovalPath(pluginsRoot, id)
	if err != nil {
		return nil, err
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("approval directory must not be a symlink")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("approval must be a regular file")
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, manifest.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	var approval InstallApproval
	if err := manifest.DecodeExtension(raw, &approval); err != nil {
		return nil, err
	}
	if approval.SchemaVersion != 1 || approval.Review.ID != id || approval.Review.Digest() != approval.ReviewDigest {
		return nil, fmt.Errorf("invalid install approval")
	}
	return &approval, nil
}

func SaveInstallApproval(pluginsRoot string, review InstallReview, acceptedDigest string) error {
	if acceptedDigest != review.Digest() {
		return fmt.Errorf("install review changed; review the staged bundle again")
	}
	path, err := ApprovalPath(pluginsRoot, review.ID)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if mkdirErr := os.MkdirAll(directory, 0700); mkdirErr != nil {
		return mkdirErr
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("approval directory must not be a symlink")
	}
	approval := InstallApproval{SchemaVersion: 1, Review: review, ReviewDigest: acceptedDigest}
	raw, err := json.Marshal(approval)
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, raw, 0600)
}

func VerifyInstallApproval(ctx context.Context, directory string) (*InstallApproval, error) {
	review, err := BuildInstallReview(ctx, directory)
	if err != nil {
		return nil, err
	}
	approved, err := ReadInstallApproval(filepath.Dir(directory), review.ID)
	if err != nil {
		return nil, fmt.Errorf("plugin %q requires install review: %w", review.ID, err)
	}
	if approved.ReviewDigest != review.Digest() {
		return nil, fmt.Errorf("plugin %q bundle changed; install again and review the changes", review.ID)
	}
	return approved, nil
}

// ReviewedLaunch contains only grants and configuration accepted for these bytes.
type ReviewedLaunch struct {
	Config       map[string]string
	Secrets      []string
	Environment  []string
	Granted      []string
	ReviewDigest string
}

// PluginSecretKey confines keychain lookups to this plugin's declared secret.
func PluginSecretKey(id, name string) string { return "plugin:" + id + ":" + name }

func ResolveReviewedLaunch(ctx context.Context, directory string, overrides ...map[string]string) (ReviewedLaunch, error) {
	approval, err := VerifyInstallApproval(ctx, directory)
	if err != nil {
		return ReviewedLaunch{}, err
	}
	declaration, err := ParseManifest(filepath.Join(directory, "plugin.yaml"))
	if err != nil {
		return ReviewedLaunch{}, err
	}
	config, err := NewPluginConfig(declaration.ID, directory)
	if err != nil {
		return ReviewedLaunch{}, err
	}
	for _, values := range overrides {
		for key, value := range values {
			if entry, declared := declaration.Config[key]; declared && !entry.Secret {
				config.overrides[key] = value
			}
		}
	}
	result := ReviewedLaunch{Config: map[string]string{}, ReviewDigest: approval.ReviewDigest}
	for name, entry := range declaration.Config {
		var value string
		if entry.Secret {
			if entry.EnvVar != "" {
				value = os.Getenv(entry.EnvVar)
			}
			if value == "" {
				value = secrets.Get(PluginSecretKey(declaration.ID, name))
			}
			if value == "" && entry.Required {
				return ReviewedLaunch{}, fmt.Errorf("required secret %q is unavailable for plugin %q", name, declaration.ID)
			}
		} else {
			value, err = config.Get(name)
			if err != nil {
				return ReviewedLaunch{}, err
			}
		}
		result.Config[name] = value
		// All explicitly resolved environment values are scrubbed too: a secret
		// cannot evade redaction by being declared as an ordinary config field.
		if value != "" && (entry.Secret || entry.EnvVar != "") {
			result.Secrets = append(result.Secrets, value)
		}
	}
	for _, request := range approval.Review.Capabilities {
		keys, known := capabilityEnvironment[request.Name]
		if !known {
			continue
		}
		result.Granted = append(result.Granted, request.Name)
		for _, key := range keys {
			if value := os.Getenv(key); value != "" {
				result.Environment = append(result.Environment, key+"="+value)
				result.Secrets = append(result.Secrets, value)
			}
		}
	}
	return result, nil
}

// CheckAcceptedBundle also pins a running manager to its original review.
// Replacing both bundle and receipt cannot make an old manager restart it.
func CheckAcceptedBundle(ctx context.Context, directory, expectedReview string) error {
	approval, err := VerifyInstallApproval(ctx, directory)
	if err != nil {
		return err
	}
	if approval.ReviewDigest != expectedReview {
		return fmt.Errorf("plugin review changed; reload the plugin")
	}
	return nil
}

// ReplaceInstallApproval writes the new acceptance while an install lock is
// held, returning a rollback for a failed directory commit.
func ReplaceInstallApproval(pluginsRoot string, review InstallReview, digest string) (func() error, error) {
	previous, err := ReadInstallApproval(pluginsRoot, review.ID)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err := SaveInstallApproval(pluginsRoot, review, digest); err != nil {
		return nil, err
	}
	return func() error {
		current, err := ReadInstallApproval(pluginsRoot, review.ID)
		if err != nil {
			return err
		}
		if current.ReviewDigest != digest {
			return fmt.Errorf("approval changed while rolling back install")
		}
		if previous != nil {
			return SaveInstallApproval(pluginsRoot, previous.Review, previous.ReviewDigest)
		}
		path, err := ApprovalPath(pluginsRoot, review.ID)
		if err != nil {
			return err
		}
		return os.Remove(path)
	}, nil
}

var ErrInstallReviewRequired = errors.New("plugin installation requires review")

type ReviewRequiredError struct {
	Review   InstallReview
	Previous *InstallApproval
}

func (review *ReviewRequiredError) Error() string { return ErrInstallReviewRequired.Error() }
func (review *ReviewRequiredError) Unwrap() error { return ErrInstallReviewRequired }

// prepareReviewedSettings exposes declarations to the settings UI only after
// an operator has accepted the installed bundle. Secret values stay in keychain.
func (host *Host) prepareReviewedSettings(ctx context.Context, declaration *manifest.Manifest) (map[string]string, error) {
	host.mu.RLock()
	database := host.store
	host.mu.RUnlock()
	if database == nil {
		return nil, nil
	}
	fields := make([]store.ConfigField, 0, len(declaration.Config.Fields)+len(declaration.Config.Secrets))
	for key, field := range declaration.Config.Fields {
		kind := field.Type
		if kind == "boolean" {
			kind = "bool"
		}
		if kind == "integer" {
			kind = "int"
		}
		fields = append(fields, store.ConfigField{Key: key, Type: kind, Label: field.Label, Description: field.Description, Default: field.Default, Required: field.Required, Options: append([]string(nil), field.Options...)})
	}
	for key, secret := range declaration.Config.Secrets {
		fields = append(fields, store.ConfigField{Key: key, Type: "secret", Label: secret.Label, Description: secret.Description, Required: secret.Required})
	}
	slices.SortFunc(fields, func(a, b store.ConfigField) int { return strings.Compare(a.Key, b.Key) })
	if err := database.UpsertPluginSchema(ctx, declaration.ID, fields); err != nil {
		return nil, err
	}
	settings, err := database.GetPluginSettings(ctx, declaration.ID)
	if err != nil {
		return nil, err
	}
	overrides := map[string]string{}
	for key, value := range settings.Settings {
		if _, declared := declaration.Config.Fields[key]; declared {
			overrides[key] = fmt.Sprint(value)
		}
	}
	return overrides, nil
}
