package apiauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// WriteTokenFile writes a secret to path with mode 0600, atomically.
func WriteTokenFile(path, secret string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create token directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".operator-token-*")
	if err != nil {
		return fmt.Errorf("create token file: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(secret + "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// ErrSecretFileExists is returned by WriteSecretFile when it must not overwrite.
var ErrSecretFileExists = errors.New("the output file already exists")

// WriteSecretFile writes a secret to path with mode 0600. The file is created
// complete or not at all (written to a temp file in the same directory, then
// linked or renamed into place), so a reader never sees a partial secret. With
// overwrite false an existing path is refused and left untouched. The secret
// never appears in an error.
func WriteSecretFile(path, secret string, overwrite bool) error {
	if !overwrite {
		if _, err := os.Lstat(path); err == nil {
			return ErrSecretFileExists
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create directory for the output file: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".secret-*")
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set output file mode: %w", err)
	}
	if _, err := tmp.WriteString(secret + "\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write output file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write output file: %w", err)
	}
	if overwrite {
		return os.Rename(name, path)
	}
	// Link fails if path appeared meanwhile, which closes the check-then-write race.
	if err := os.Link(name, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrSecretFileExists
		}
		return fmt.Errorf("write output file: %w", err)
	}
	return nil
}

// ReadTokenFile returns the trimmed secret stored at path.
func ReadTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is the operator token file location resolved by the caller
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// EnvTokenVar is the explicit operator-token override for headless runs.
const EnvTokenVar = "NANITE_AUTH_TOKEN" // #nosec G101 -- an environment variable name, not a credential

// EnsureOperator guarantees an operator credential exists. With override set
// (the NANITE_AUTH_TOKEN value) that secret is imported and no file is
// written. Otherwise, when no active operator credential exists, one is
// created and its secret is written once to tokenPath with mode 0600; only
// the path is logged, never the secret. It reports whether it created one.
func EnsureOperator(ctx context.Context, s *Store, tokenPath, override string) (bool, error) {
	if override != "" {
		if _, err := s.ImportSecret(ctx, override, "env-override"); err != nil {
			return false, err
		}
		return false, nil
	}
	has, err := s.HasActiveOperator(ctx)
	if err != nil {
		return false, err
	}
	if has {
		return false, nil
	}
	if tokenPath == "" {
		return false, errors.New("no operator credential and no token file path")
	}
	_, secret, err := s.Create(ctx, CreateParams{Kind: KindOperator, Label: "operator", CreatedBy: "bootstrap"})
	if err != nil {
		return false, err
	}
	if err := WriteTokenFile(tokenPath, secret); err != nil {
		return false, fmt.Errorf("write operator token file: %w", err)
	}
	slog.Info("created the operator credential", "token_file", tokenPath, "note", "the secret is in that file (mode 0600); it is not logged")
	return true, nil
}

// ResetOperator revokes every operator credential and issues a fresh one,
// writing its secret to tokenPath. It is the offline recovery path (the
// server need not be running).
func ResetOperator(ctx context.Context, s *Store, tokenPath string) error {
	creds, err := s.List(ctx, false)
	if err != nil {
		return err
	}
	for _, c := range creds {
		if c.Kind == KindOperator {
			if _, revokeErr := s.Revoke(ctx, c.ID); revokeErr != nil {
				return revokeErr
			}
		}
	}
	_, secret, err := s.Create(ctx, CreateParams{Kind: KindOperator, Label: "operator", CreatedBy: "reset"})
	if err != nil {
		return err
	}
	return WriteTokenFile(tokenPath, secret)
}

// ClientToken resolves the credential a local client (CLI, autostart, mcp)
// presents: the NANITE_AUTH_TOKEN override, else the operator token file.
func ClientToken(tokenPath string) (string, error) {
	if v := os.Getenv(EnvTokenVar); v != "" {
		return v, nil
	}
	if tokenPath == "" {
		return "", errors.New("no credential: set NANITE_AUTH_TOKEN or run `nanite serve` once to create the operator token file")
	}
	secret, err := ReadTokenFile(tokenPath)
	if err != nil {
		return "", fmt.Errorf("read operator token file %s: %w (run `nanite serve` once, or `nanite auth reset-operator`)", tokenPath, err)
	}
	return secret, nil
}
