package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/secrets"
	"github.com/zalando/go-keyring"
)

func reviewBundle(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, "example.plugin")
	if checkErr := os.MkdirAll(directory, 0700); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(sharedManifestBytes(t)), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := os.WriteFile(filepath.Join(directory, "asset.txt"), []byte("original"), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	return root, directory
}

func TestInstallReviewRequiresExactAcceptedBundle(t *testing.T) {
	root, directory := reviewBundle(t)
	ctx := context.Background()
	if _, checkErr := VerifyInstallApproval(ctx, directory); checkErr == nil {
		t.Fatal("unapproved bundle accepted")
	}
	review, err := BuildInstallReview(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Secrets) != 1 || review.Secrets[0].Name != "token" || review.Secrets[0].Environment != "EXAMPLE_TOKEN" {
		t.Fatalf("secret review: %+v", review.Secrets)
	}
	if checkErr := SaveInstallApproval(root, review, "wrong digest"); checkErr == nil {
		t.Fatal("unreviewed digest accepted")
	}
	if checkErr := SaveInstallApproval(root, review, review.Digest()); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := VerifyInstallApproval(ctx, directory); checkErr != nil {
		t.Fatal(checkErr)
	}
	// The approval itself is outside the digested bundle.
	digest, err := BundleDigest(ctx, directory)
	if err != nil || digest != review.BundleDigest {
		t.Fatalf("approval changed bundle: %s %v", digest, err)
	}
	if checkErr := os.WriteFile(filepath.Join(directory, "asset.txt"), []byte("changed"), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := VerifyInstallApproval(ctx, directory); checkErr == nil {
		t.Fatal("changed asset accepted")
	}
}

func TestBundleDigestCoversNamesPermissionsAndEveryFile(t *testing.T) {
	_, directory := reviewBundle(t)
	ctx := context.Background()
	baseline, err := BundleDigest(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	asset := filepath.Join(directory, "asset.txt")
	if checkErr := os.Rename(asset, filepath.Join(directory, "renamed.txt")); checkErr != nil {
		t.Fatal(checkErr)
	}
	renamed, err := BundleDigest(ctx, directory)
	if err != nil || renamed == baseline {
		t.Fatalf("rename unchanged: %v", err)
	}
	if checkErr := os.Chmod(filepath.Join(directory, "renamed.txt"), 0400); checkErr != nil {
		t.Fatal(checkErr)
	}
	permissions, err := BundleDigest(ctx, directory)
	if err != nil || permissions == renamed {
		t.Fatalf("mode unchanged: %v", err)
	}
	if checkErr := os.WriteFile(filepath.Join(directory, "extra.txt"), []byte("extra"), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	added, err := BundleDigest(ctx, directory)
	if err != nil || added == permissions {
		t.Fatalf("new file unchanged: %v", err)
	}
	if checkErr := os.Symlink("extra.txt", filepath.Join(directory, "link")); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := BundleDigest(ctx, directory); checkErr == nil {
		t.Fatal("symlink accepted")
	}
}

func TestInstallReviewRejectsUnknownRequiredCapability(t *testing.T) {
	_, directory := reviewBundle(t)
	raw := strings.Replace(sharedManifestBytes(t), `"runtime": "subprocess"`, `"capabilities":[{"name":"unknown","reason":"test"}],"runtime": "subprocess"`, 1)
	if checkErr := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(raw), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := BuildInstallReview(context.Background(), directory); checkErr == nil {
		t.Fatal("unknown required capability accepted")
	}
	raw = strings.Replace(raw, `"reason":"test"`, `"reason":"test","optional":true`, 1)
	if checkErr := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(raw), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := BuildInstallReview(context.Background(), directory); checkErr != nil {
		t.Fatal(checkErr)
	}
}

func TestBundleDigestCancellation(t *testing.T) {
	_, directory := reviewBundle(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, checkErr := BundleDigest(ctx, directory); checkErr == nil {
		t.Fatal("canceled digest succeeded")
	}
}

func TestReviewedLaunchResolvesOnlyDeclaredPluginSecretsAndCapabilities(t *testing.T) {
	keyring.MockInit()
	root, directory := reviewBundle(t)
	t.Setenv("EXAMPLE_TOKEN", "")
	t.Setenv("UNDECLARED_KEY", "host-private")
	t.Setenv("SSH_AUTH_SOCK", "/private/ssh")
	t.Setenv("DOCKER_HOST", "private-docker")
	if checkErr := secrets.Set(PluginSecretKey("another.plugin", "token"), "wrong-plugin"); checkErr != nil {
		t.Fatal(checkErr)
	}
	raw := strings.Replace(sharedManifestBytes(t), `"runtime": "subprocess"`, `"capabilities":[{"name":"ssh_agent","reason":"SSH access"}],"runtime": "subprocess"`, 1)
	if checkErr := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(raw), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	review, err := BuildInstallReview(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := SaveInstallApproval(root, review, review.Digest()); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, checkErr := ResolveReviewedLaunch(context.Background(), directory); checkErr == nil {
		t.Fatal("another plugin's keychain credential accepted")
	}
	if checkErr := secrets.Set(PluginSecretKey("example.plugin", "token"), "keychain-secret"); checkErr != nil {
		t.Fatal(checkErr)
	}
	launch, err := ResolveReviewedLaunch(context.Background(), directory, map[string]string{"enabled": "true", "token": "ignored-database-secret", "undeclared": "host-private"})
	if err != nil {
		t.Fatal(err)
	}
	if launch.Config["token"] != "keychain-secret" || launch.Config["enabled"] != "true" || len(launch.Config) != 2 {
		t.Fatalf("config: %+v", launch.Config)
	}
	if !slices.Equal(launch.Environment, []string{"SSH_AUTH_SOCK=/private/ssh"}) || !slices.Equal(launch.Granted, []string{"ssh_agent"}) {
		t.Fatalf("grant: %+v", launch)
	}
	if !slices.Contains(launch.Secrets, "keychain-secret") || !slices.Contains(launch.Secrets, "/private/ssh") {
		t.Fatal("resolved values not scrubbed")
	}
	t.Setenv("EXAMPLE_TOKEN", "environment-secret")
	launch, err = ResolveReviewedLaunch(context.Background(), directory)
	if err != nil || launch.Config["token"] != "environment-secret" || !slices.Contains(launch.Secrets, "environment-secret") {
		t.Fatalf("environment secret: %v", err)
	}
}

func TestApprovalRollbackRestoresReceiptAndPinsOldManager(t *testing.T) {
	root, directory := reviewBundle(t)
	ctx := context.Background()
	original, err := BuildInstallReview(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := SaveInstallApproval(root, original, original.Digest()); checkErr != nil {
		t.Fatal(checkErr)
	}
	if checkErr := os.WriteFile(filepath.Join(directory, "asset.txt"), []byte("upgrade"), 0600); checkErr != nil {
		t.Fatal(checkErr)
	}
	updated, err := BuildInstallReview(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	rollback, err := ReplaceInstallApproval(root, updated, updated.Digest())
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := CheckAcceptedBundle(ctx, directory, original.Digest()); checkErr == nil {
		t.Fatal("old manager accepted replacement bundle and receipt")
	}
	if checkErr := rollback(); checkErr != nil {
		t.Fatal(checkErr)
	}
	previous, err := ReadInstallApproval(root, original.ID)
	if err != nil || previous.ReviewDigest != original.Digest() {
		t.Fatalf("rollback: %v", err)
	}
	path, err := ApprovalPath(root, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := os.Remove(path); checkErr != nil {
		t.Fatal(checkErr)
	}
	rollback, err = ReplaceInstallApproval(root, updated, updated.Digest())
	if err != nil {
		t.Fatal(err)
	}
	if checkErr := rollback(); checkErr != nil {
		t.Fatal(checkErr)
	}
	if _, err := ReadInstallApproval(root, original.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new receipt retained: %v", err)
	}
}

func TestBundleDigestRefusesOversizedFileBeforeReading(t *testing.T) {
	_, directory := reviewBundle(t)
	file, err := os.Create(filepath.Join(directory, "oversized")) // #nosec G304 -- fixed sparse-file fixture under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if truncateErr := file.Truncate(MaxBundleBytes + 1); truncateErr != nil {
		t.Fatal(truncateErr)
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if _, digestErr := BundleDigest(context.Background(), directory); digestErr == nil {
		t.Fatal("oversized bundle accepted")
	}
}
