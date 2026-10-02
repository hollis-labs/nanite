package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/plugin"
)

func TestReviewedInstallerRefusalStaleDigestAndCommitRollback(t *testing.T) {
	source := setupPlugin(t, validManifest(), map[string]string{"bin/giphy": "#!/bin/sh\n", "envelopes/giphy-modal.schema.json": "{}"})
	root := t.TempDir()
	options := BuildOptions{Extractor: &TarGzExtractor{}, Loader: &fakeLoader{}, PluginsRoot: filepath.Join(root, "plugins"), StagingRoot: filepath.Join(root, "staging")}
	var preview plugin.InstallReview
	options.Review = func(_ context.Context, review plugin.InstallReview, previous *plugin.InstallApproval) (string, error) {
		preview = review
		return "", &plugin.ReviewRequiredError{Review: review, Previous: previous}
	}
	installer, _ := NewInstaller(options)
	src := &fakeSource{id: "giphy", handle: Handle{Kind: "directory", Path: source}}
	if _, err := installer.Install(context.Background(), src); !errors.Is(err, plugin.ErrInstallReviewRequired) {
		t.Fatalf("preview: %v", err)
	}
	if installer.State() != StateAwaitingReview {
		t.Fatalf("preview state: %s", installer.State())
	}
	if _, err := os.Stat(filepath.Join(options.PluginsRoot, "giphy")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preview committed bundle")
	}
	options.Review = func(_ context.Context, review plugin.InstallReview, _ *plugin.InstallApproval) (string, error) {
		return preview.Digest(), nil
	}
	installer, _ = NewInstaller(options)
	directory, err := installer.Install(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	original, err := plugin.VerifyInstallApproval(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, "ui/dist/index.js"), "changed asset")
	installer, _ = NewInstaller(options)
	if _, checkErr := installer.Install(context.Background(), src); checkErr == nil {
		t.Fatal("stale review approved changed source")
	}
	if _, checkErr := plugin.VerifyInstallApproval(context.Background(), directory); checkErr != nil {
		t.Fatalf("refusal changed original bundle/receipt: %v", checkErr)
	}
	options.Review = func(_ context.Context, review plugin.InstallReview, previous *plugin.InstallApproval) (string, error) {
		if previous == nil || previous.ReviewDigest != original.ReviewDigest || review.BundleDigest == previous.Review.BundleDigest {
			t.Fatal("upgrade review lacks previous/change")
		}
		return review.Digest(), nil
	}
	installer, staging := NewInstaller(options)
	installer.Staging = failingReviewedCommit{DirStaging: staging}
	if _, checkErr := installer.Install(context.Background(), src); checkErr == nil {
		t.Fatal("commit failure accepted")
	}
	if _, checkErr := plugin.VerifyInstallApproval(context.Background(), directory); checkErr != nil {
		t.Fatalf("rollback lost original approval: %v", checkErr)
	}
}

type failingReviewedCommit struct{ *DirStaging }

func (failingReviewedCommit) Commit(context.Context, string, string) (string, error) {
	return "", errors.New("commit failure")
}

func TestDirectoryExtractionBoundsAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name      string
		extractor TarGzExtractor
	}{
		{"file", TarGzExtractor{MaxFileBytes: 3}},
		{"total", TarGzExtractor{MaxTotalUncompressedBytes: 6}},
		{"entries", TarGzExtractor{MaxEntries: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, target := t.TempDir(), t.TempDir()
			writeFile(t, filepath.Join(source, "one"), "1234")
			writeFile(t, filepath.Join(source, "two"), "1234")
			if err := test.extractor.Extract(context.Background(), Handle{Kind: "directory", Path: source}, target, nil); err == nil {
				t.Fatal("unbounded directory accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (&TarGzExtractor{}).Extract(ctx, Handle{Kind: "directory", Path: t.TempDir()}, t.TempDir(), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
