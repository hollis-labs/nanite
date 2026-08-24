package secrets

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func captureKeyringLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	return &logs
}

func TestGetLogsMissingKeyAtDebug(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	logs := captureKeyringLogs(t)

	if got := Get("missing-provider-key"); got != "" {
		t.Fatalf("Get = %q, want empty string for a missing key", got)
	}
	logOutput := logs.String()
	for _, want := range []string{
		`"level":"DEBUG"`,
		`"msg":"secrets: key not found"`,
		`"key":"missing-provider-key"`,
	} {
		if !strings.Contains(logOutput, want) {
			t.Errorf("debug log %q missing from %s", want, logOutput)
		}
	}
}

func TestGetLogsKeyringAccessErrorAtWarn(t *testing.T) {
	keyringErr := errors.New("keychain unavailable")
	keyring.MockInitWithError(keyringErr)
	t.Cleanup(keyring.MockInit)
	logs := captureKeyringLogs(t)

	if got := Get("provider-api-key:anthropic"); got != "" {
		t.Fatalf("Get = %q, want empty string for a keyring access error", got)
	}
	logOutput := logs.String()
	for _, want := range []string{
		`"level":"WARN"`,
		`"msg":"secrets: get failed"`,
		`"key":"provider-api-key:anthropic"`,
		`"err":"` + keyringErr.Error() + `"`,
	} {
		if !strings.Contains(logOutput, want) {
			t.Errorf("warning log %q missing from %s", want, logOutput)
		}
	}
}

func TestGetDoesNotLogSecretValue(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)
	logs := captureKeyringLogs(t)
	const secret = "credential-that-must-not-be-logged"
	if err := keyring.Set(serviceName, "provider-api-key:test", secret); err != nil {
		t.Fatalf("keyring.Set: %v", err)
	}

	if got := Get("provider-api-key:test"); got != secret {
		t.Fatalf("Get = %q, want stored secret", got)
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatalf("secret value appeared in logs: %s", logs.String())
	}
}
