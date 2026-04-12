// Package otel wraps the external feotel library, honoring
// NANITE_OTEL_DISABLED=1 and Config.Disabled to skip exporter init.
// Returns a no-op tracer provider when disabled so callers of
// otel.Tracer(...) don't need disabled-aware branches.
//
// Precedence: the NANITE_OTEL_DISABLED=1 environment variable takes
// precedence over Config.Disabled. Either set disables exporter init.
//
// Note on logging: the repo-wide slog handler is not yet installed
// (see audit 03 — log.Printf is the prevailing pattern). This package
// uses log.Printf to match the surrounding codebase. The parallel
// slog-migration session will convert this alongside the rest of the
// repo.
package otel

import (
	"context"
	"log"
	"os"

	feotel "github.com/hollis-labs/go-otel"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"
)

// disabledEnvVar is the environment variable that forces the no-op
// tracer provider regardless of Config.Disabled.
const disabledEnvVar = "NANITE_OTEL_DISABLED"

// Config controls OTel initialisation.
type Config struct {
	// ServiceName is forwarded to feotel via WithServiceName.
	ServiceName string
	// Disabled, when true, installs a no-op tracer provider and skips
	// exporter init. NANITE_OTEL_DISABLED=1 has the same effect and
	// takes precedence when set.
	Disabled bool
}

// noopShutdown is the shutdown sentinel returned when the no-op
// provider is installed. It is a package-level var so tests can
// compare shutdown-func identity (via reflect.ValueOf(...).Pointer()).
var noopShutdown = func(context.Context) error { return nil }

// Init installs either a real OTel tracer provider (via feotel) or a
// no-op tracer provider, depending on env var + cfg.
//
// When disabled:
//   - installs noop.NewTracerProvider() via otel.SetTracerProvider
//   - returns a no-op shutdown func (returns nil)
//   - returns a nil error
//
// When enabled:
//   - delegates to feotel.Init with WithServiceName(cfg.ServiceName)
//   - returns feotel's shutdown + any init error
func Init(ctx context.Context, cfg Config) (shutdown func(context.Context) error, err error) {
	envDisabled := os.Getenv(disabledEnvVar) == "1"
	if envDisabled || cfg.Disabled {
		otel.SetTracerProvider(noop.NewTracerProvider())
		source := "config"
		if envDisabled {
			source = "env"
		}
		// env+cfg gate: see internal/otel/otel.go (Init — this block).
		log.Printf("otel: disabled (source=%s); no-op tracer provider installed", source)
		return noopShutdown, nil
	}

	log.Printf("otel: enabled; service=%s", cfg.ServiceName)
	return feotel.Init(ctx, feotel.WithServiceName(cfg.ServiceName))
}
