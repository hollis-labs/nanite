package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/hollis-labs/nanite/internal/llm/keycheck"
	"github.com/hollis-labs/substrate/harness/adapters/provider"
)

// Provider check statuses reported by ProviderConfigService.TestConnection.
const (
	ProviderCheckAccepted    = "accepted"
	ProviderCheckRejected    = "rejected"
	ProviderCheckUnreachable = "unreachable"
	ProviderCheckNoKey       = "no_key"
	ProviderCheckUnsupported = "unsupported"
	ProviderCheckCLIFound    = "cli_found"
	ProviderCheckCLINotFound = "cli_not_found"
)

// providerCheckTimeout bounds one connection check end to end.
const providerCheckTimeout = 10 * time.Second

// ProviderCheckResult is the outcome of one provider connection check.
type ProviderCheckResult struct {
	// OK is true only for ProviderCheckAccepted and ProviderCheckCLIFound.
	OK      bool
	Status  string
	Message string
	// Path is the resolved CLI binary for a CLI provider row.
	Path string
}

// keyVerifier is the one method TestConnection needs from an API adapter:
// an authenticated call that costs no tokens.
type keyVerifier interface {
	VerifyKey(ctx context.Context) error
}

// CLIDetection is the auto-detection status of one CLI adapter.
type CLIDetection struct {
	Name         string // adapter name (claude, codex, opencode)
	ProviderType string // pty, pty-codex, pty-opencode
	ProviderID   string // seeded provider row id
	Detected     bool
	Path         string // resolved path if found
	EnvVar       string // env var for override
}

// TestConnection checks whether a provider row is usable right now:
//   - an API provider Nanite has an adapter for: its resolved key (keychain,
//     then environment — the key startup would use) is accepted by the
//     provider's API, via a call that costs no tokens;
//   - a CLI provider: its binary is found and executable. Whether the CLI is
//     logged in cannot be checked without running it, so it is not claimed;
//   - anything else: not supported in this build, which is also why a key
//     saved for it has no effect.
//
// An unknown row id returns the store's error. Every check outcome, including
// a rejected key or an unreachable provider, is a result, not an error.
func (s *ProviderConfigService) TestConnection(ctx context.Context, id string) (ProviderCheckResult, error) {
	row, err := s.store.GetProvider(ctx, id)
	if err != nil {
		return ProviderCheckResult{}, err
	}

	if spec, ok := APIProviderSpecByID(row.ID); ok {
		return s.checkAPIKey(ctx, spec), nil
	}
	for _, d := range s.DetectCLIs(ctx) {
		if d.ProviderID == row.ID {
			if d.Detected {
				return ProviderCheckResult{OK: true, Status: ProviderCheckCLIFound, Path: d.Path,
					Message: fmt.Sprintf("%s CLI found at %s", d.Name, d.Path)}, nil
			}
			return ProviderCheckResult{Status: ProviderCheckCLINotFound,
				Message: fmt.Sprintf("%s CLI not found; set %s or install it on PATH", d.Name, d.EnvVar)}, nil
		}
	}
	return ProviderCheckResult{Status: ProviderCheckUnsupported,
		Message: "Nanite has no adapter for this provider in this build, so it cannot be checked or used"}, nil
}

func (s *ProviderConfigService) checkAPIKey(ctx context.Context, spec APIProviderSpec) ProviderCheckResult {
	key, _ := s.resolveKey(spec.ProviderID, spec.EnvKey)
	if key == "" {
		return ProviderCheckResult{Status: ProviderCheckNoKey,
			Message: fmt.Sprintf("No API key is set for %s", spec.DisplayName)}
	}

	ctx, cancel := context.WithTimeout(ctx, providerCheckTimeout)
	defer cancel()
	err := s.newVerifier(spec, key).VerifyKey(ctx)
	switch {
	case err == nil:
		return ProviderCheckResult{OK: true, Status: ProviderCheckAccepted,
			Message: fmt.Sprintf("Key accepted by %s", spec.DisplayName)}
	case errors.Is(err, keycheck.ErrRejected):
		return ProviderCheckResult{Status: ProviderCheckRejected,
			Message: fmt.Sprintf("%s rejected the key", spec.DisplayName)}
	default:
		return ProviderCheckResult{Status: ProviderCheckUnreachable,
			Message: fmt.Sprintf("Could not reach %s: %v", spec.DisplayName, err)}
	}
}

// defaultKeyVerifier checks a key on a fresh, throwaway adapter, so a check
// never touches the registered adapter's rate tracker or circuit breaker.
func defaultKeyVerifier(spec APIProviderSpec, key string) keyVerifier {
	p := spec.NewProvider(key)
	if v, ok := p.(keyVerifier); ok {
		return v
	}
	return unverifiable{spec.Name}
}

type unverifiable struct{ name string }

func (u unverifiable) VerifyKey(context.Context) error {
	return fmt.Errorf("the %s adapter cannot verify a key", u.name)
}

// DetectCLIs runs auto-detection for all CLI adapters using the same
// Detect() logic that the runtime uses at startup. A cli_path in the
// provider row's settings takes precedence.
func (s *ProviderConfigService) DetectCLIs(ctx context.Context) []CLIDetection {
	type cliSpec struct {
		adapter                  provider.CLIAdapter
		provType, provID, envVar string
	}

	// CW-20260508-0010: detection list mirrors the production CLIAdapter
	// slice in cmd/nanite/main.go — claude / codex / opencode only.
	// gemini/copilot/aider/junie/kiro/qwen were never reached by any
	// production code path (factory.shouldUsePTY filters to claude shapes;
	// codex/opencode use the SubprocessBridge path).
	specs := []cliSpec{
		{provider.NewClaudeAdapter(), "pty", "pty-001", "CLAUDE_CLI_PATH"},
		{provider.NewCodexAdapter(), "pty-codex", "pty-codex-001", "CODEX_CLI_PATH"},
		{provider.NewOpencodeAdapter(), "pty-opencode", "pty-opencode-001", "OPENCODE_CLI_PATH"},
	}

	results := make([]CLIDetection, 0, len(specs))
	for _, spec := range specs {
		result := CLIDetection{
			Name:         spec.adapter.Name(),
			ProviderType: spec.provType,
			ProviderID:   spec.provID,
			EnvVar:       spec.envVar,
		}

		// Check if a custom path is stored in provider settings.
		if p, err := s.store.GetProvider(ctx, spec.provID); err == nil && p.Settings != "" && p.Settings != "{}" {
			var settings map[string]string
			if json.Unmarshal([]byte(p.Settings), &settings) == nil {
				if cp, ok := settings["cli_path"]; ok && cp != "" {
					result.Path = cp
					result.Detected = isExecutable(cp)
					results = append(results, result)
					continue
				}
			}
		}

		// Use the adapter's own Detect() — same logic as runtime registration.
		if path, ok := spec.adapter.Detect(); ok {
			result.Path = path
			result.Detected = true
		}
		results = append(results, result)
	}
	return results
}

// isExecutable checks if a file exists and is executable.
func isExecutable(path string) bool {
	// Absolute or relative path — check the file directly.
	if strings.Contains(path, "/") || strings.Contains(path, "\\") {
		info, err := os.Stat(path)
		if err != nil {
			return false
		}
		return !info.IsDir() && info.Mode()&0111 != 0
	}
	// Bare binary name — search PATH.
	_, err := exec.LookPath(path)
	return err == nil
}
