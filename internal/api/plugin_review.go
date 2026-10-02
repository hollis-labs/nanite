package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"

	naniteplugin "github.com/hollis-labs/nanite/internal/plugin"
	"github.com/hollis-labs/nanite/internal/plugin/install"
)

func acceptedInstallReview(digest string) install.Reviewer {
	return func(ctx context.Context, review naniteplugin.InstallReview, previous *naniteplugin.InstallApproval) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if digest != review.Digest() {
			return "", &naniteplugin.ReviewRequiredError{Review: review, Previous: previous}
		}
		return digest, nil
	}
}

func writeInstallReview(w http.ResponseWriter, err error) bool {
	var review *naniteplugin.ReviewRequiredError
	if !errors.As(err, &review) {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "review_required", "review": review.Review, "review_digest": review.Review.Digest(), "previous": review.Previous})
	return true
}

type directoryPluginSource struct{ id, path string }

func (source directoryPluginSource) PluginID() string { return source.id }
func (source directoryPluginSource) Download(context.Context, string, install.EventFunc) (install.Handle, error) {
	return install.Handle{Kind: "directory", Path: source.path}, nil
}

func (pms *pluginManagerState) installReviewedDirectory(ctx context.Context, directory, id, digest string) (string, error) {
	installer, _ := install.NewInstaller(install.BuildOptions{Extractor: &install.TarGzExtractor{}, Loader: hostLoader{pms: pms}, StagingRoot: filepath.Join(pms.pluginsDir, ".staging"), PluginsRoot: pms.pluginsDir, Review: acceptedInstallReview(digest)})
	return installer.Install(ctx, directoryPluginSource{id: id, path: directory})
}
