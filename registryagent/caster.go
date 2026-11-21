package registryagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

const maxArtifactBytes = 72 << 20

var casReference = regexp.MustCompile(`^cas://sha256/[0-9a-f]{64}$`)

// CasterCommand reads bytes through an explicitly selected, already paired
// Caster executable. It runs only "get <cas-ref>"; neither executable nor
// arguments come from an agent record. Caster owns its credential resolution.
type CasterCommand struct{ Executable string }

func (CasterCommand) Put(context.Context, []byte) (string, error) {
	return "", fmt.Errorf("registryagent: Caster adapter is read-only")
}

func (c CasterCommand) Fetch(ctx context.Context, ref string) ([]byte, error) {
	if !filepath.IsAbs(c.Executable) || !casReference.MatchString(ref) {
		return nil, fmt.Errorf("registryagent: absolute Caster executable and SHA-256 reference required")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	output := boundedOutput{cancel: cancel}
	cmd := exec.CommandContext(ctx, c.Executable, "get", ref) // #nosec G204 -- host-selected absolute executable, fixed verb, validated CAS ref.
	cmd.Stdout = &output
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if output.exceeded {
			return nil, fmt.Errorf("registryagent: Caster artifact exceeds size limit")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("registryagent: Caster read failed")
	}
	data := output.buffer.Bytes()
	sum := sha256.Sum256(data)
	if ref != "cas://sha256/"+hex.EncodeToString(sum[:]) {
		return nil, fmt.Errorf("registryagent: Caster content hash mismatch")
	}
	return data, nil
}

type boundedOutput struct {
	buffer   bytes.Buffer
	cancel   context.CancelFunc
	exceeded bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > maxArtifactBytes-b.buffer.Len() {
		b.exceeded = true
		b.cancel()
		return 0, fmt.Errorf("artifact exceeds size limit")
	}
	return b.buffer.Write(p)
}
