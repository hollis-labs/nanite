package agentregistry

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/hollis-labs/agentkit/agentlaunch"
	"gopkg.in/yaml.v3"
)

// agent_source.go — C1's directory-facing done-criterion (locked
// decision D2): Nanite registers itself as an `agent-source` resolver
// HANDLE so other launch consumers can call back to compose a
// Nanite-owned agent.
//
// What the directory holds (D2):
//
//   - An AgentSourceContract whose Resolver is a ResolverHandle:
//     protocol http, target the loopback /api/tools/call URL, operation
//     `agent_source_resolve`. That handle is the entire payload.
//
// What the directory NEVER holds (D2):
//
//   - No system_prompt, no tools, no modes. The agent body is composed
//     on-demand by the agent_source_resolve self-tool reading Nanite's
//     own agent_profiles table; the directory only knows where to call.
//
// The registration follows the file-backed pointer model: the contract
// body is written to a local file, and a RegistrationRecord pointing at
// that file (handle + pointer, never the body inline) is registered.

// AgentSourceName is the registry object name Nanite's agent-source
// contract is published under. It is a stable single-tenant identity;
// multi-tenant owner/namespace trust is the deferred Risk-2 (out of
// scope for Phase C).
const AgentSourceName = "nanite"

// agentSourceResolveOperation is the resolver-handle operation name. It
// MUST match mcp.agentSourceResolveToolName — the directory handle and
// the self-tool dispatch agree on this exact string. It is duplicated
// here (rather than imported) to keep internal/agentregistry free of an
// internal/mcp dependency; the registration test cross-checks the two.
const agentSourceResolveOperation = "agent_source_resolve"

// AgentSourceTrust is the trust token stamped on the resolver handle's
// gate. ResolverHandle.Validate requires a non-empty gate for the http
// protocol; "loopback" records that the callback target is the
// loopback-only /api/tools/call endpoint, which is itself the trust
// boundary (see internal/api/tools_call.go).
const AgentSourceTrust = "loopback"

// RegisterAgentSource registers Nanite as an `agent-source` resolver
// handle in the shared directory registry.
//
// loopbackToolsURL is the loopback /api/tools/call URL the resolver
// handle targets (typically http://127.0.0.1:<port>/api/tools/call).
// handleDir is a writable local directory the handle-contract file is
// written into (the registration is a file-backed pointer record).
//
// D1: a registration failure is returned, NEVER fatal. The caller logs
// and continues — the registry is never mandatory for Nanite to run.
// D2: the registered record carries the AgentSourceContract handle
// only; RegisterAgentSource has no path that could inline an agent body.
func RegisterAgentSource(reg *Registry, loopbackToolsURL, handleDir string, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	if reg == nil {
		return fmt.Errorf("agentregistry: RegisterAgentSource: nil registry")
	}
	if loopbackToolsURL == "" {
		return fmt.Errorf("agentregistry: RegisterAgentSource: empty loopback tools URL")
	}

	contract := AgentSourceContract(loopbackToolsURL)
	if err := contract.Validate(); err != nil {
		return fmt.Errorf("agentregistry: agent-source contract invalid: %w", err)
	}

	// Materialize the handle contract to a local file. The registration
	// record points at this file; the file holds the handle (D2 — a
	// resolver handle, not an agent body).
	if err := os.MkdirAll(handleDir, 0o755); err != nil {
		return fmt.Errorf("agentregistry: create handle dir: %w", err)
	}
	contractPath := filepath.Join(handleDir, "agent-source-"+AgentSourceName+".yaml")
	raw, err := yaml.Marshal(contract)
	if err != nil {
		return fmt.Errorf("agentregistry: marshal agent-source contract: %w", err)
	}
	if err := os.WriteFile(contractPath, raw, 0o644); err != nil {
		return fmt.Errorf("agentregistry: write agent-source contract: %w", err)
	}

	schemaVersion, iface, ok := agentlaunch.KindRegistrationSpec(agentlaunch.RegistryKindAgentSource)
	if !ok {
		return fmt.Errorf("agentregistry: no registration spec for agent-source kind")
	}
	digest := sha256.Sum256(raw)
	record := agentlaunch.RegistrationRecord{
		Meta: agentlaunch.RegistryContractMeta{
			Ref: agentlaunch.RegistryObjectRef{
				Kind: agentlaunch.RegistryKindAgentSource,
				Name: AgentSourceName,
			},
			SchemaVersion: schemaVersion,
			Interface:     iface,
		},
		Source: agentlaunch.RegistrationSource{
			FilePath:  contractPath,
			Digest:    "sha256:" + hex.EncodeToString(digest[:]),
			Directory: handleDir,
		},
	}

	env := agentlaunch.RegistryEnvelope{
		Version:    agentlaunch.RegistryEnvelopeVersionV1,
		Resolution: agentlaunch.RegistryResolutionLocalFirst,
		Operation:  agentlaunch.RegistryOperationRegister,
		Registrar:  reg.Descriptor(),
		Register: &agentlaunch.RegisterPayload{
			Record: record,
			Upsert: true,
		},
	}
	if _, err := reg.Registrar().Handle(env); err != nil {
		return fmt.Errorf("agentregistry: register agent-source: %w", err)
	}
	log.Info("agentregistry: registered agent-source handle",
		"name", AgentSourceName,
		"resolver_target", loopbackToolsURL,
		"resolver_operation", agentSourceResolveOperation)
	return nil
}

// AgentSourceContract builds the AgentSourceContract Nanite publishes —
// a resolver handle pointing back at the agent_source_resolve self-tool.
// It is exported so tests can assert the contract shape (D2: handle
// only, no agent body) without reaching into RegisterAgentSource.
func AgentSourceContract(loopbackToolsURL string) agentlaunch.AgentSourceContract {
	return agentlaunch.AgentSourceContract{
		Meta: agentlaunch.RegistryContractMeta{
			Ref: agentlaunch.RegistryObjectRef{
				Kind: agentlaunch.RegistryKindAgentSource,
				Name: AgentSourceName,
			},
			SchemaVersion: agentlaunch.AgentSourceSchemaVersionV1,
			Interface:     agentlaunch.AgentSourceInterfaceV1,
		},
		Resolver: agentlaunch.ResolverHandle{
			Protocol:  agentlaunch.ResolverProtocolHTTP,
			Target:    loopbackToolsURL,
			Operation: agentSourceResolveOperation,
			Gate:      agentlaunch.TrustGate{Trust: AgentSourceTrust},
		},
		// Lazy: consumers resolve the agent body on demand via the
		// handle. Nanite never snapshots a body into the directory.
		Freshness: agentlaunch.SourceFreshnessLazy,
	}
}
