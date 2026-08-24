// Package otel wraps the external feotel library, honoring
// NANITE_OTEL_DISABLED=1 and Config.Disabled to skip exporter init.
// Returns a no-op tracer provider when disabled so callers of
// otel.Tracer(...) don't need disabled-aware branches.
//
// Precedence: the NANITE_OTEL_DISABLED=1 environment variable takes
// precedence over Config.Disabled. Either set disables exporter init.
package otel

import (
	"context"
	"log/slog"
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
		slog.Info("otel: disabled, no-op tracer provider installed", "source", source)
		return noopShutdown, nil
	}

	slog.Info("otel: enabled", "service", cfg.ServiceName)
	return feotel.Init(ctx, feotel.WithServiceName(cfg.ServiceName))
}
