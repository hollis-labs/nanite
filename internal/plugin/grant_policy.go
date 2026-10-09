package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/capability"
	"github.com/hollis-labs/nanite/internal/plugin/subprocess"
)

const DefaultPluginGrantLifetime = 24 * time.Hour
const PluginGrantLifetimeEnv = "NANITE_PLUGIN_GRANT_LIFETIME"

// pluginGrantPolicy is explicit app policy, independently of a declaration or
// browser metadata. Invalid configuration refuses launch rather than granting
// an infinite lifetime. No persisted settings are changed.
func pluginGrantPolicy(review string) (time.Duration, string, error) {
	lifetime := DefaultPluginGrantLifetime
	if raw := os.Getenv(PluginGrantLifetimeEnv); raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return 0, "", fmt.Errorf("invalid plugin grant lifetime policy")
		}
		lifetime = value
	}
	digest := sha256.Sum256([]byte("nanite-plugin-policy-v2\n" + review + "\n" + lifetime.String()))
	return lifetime, hex.EncodeToString(digest[:]), nil
}

// issueReviewedGrants maps only accepted declarations to separately versioned
// host descriptors. Raw Grant.Scope carries these exact typed DTOs; the shared
// normalized capability.Scope vocabulary does not reinterpret them.
func issueReviewedGrants(ctx context.Context, directory string, launch ReviewedLaunch, runtime capability.RuntimeIdentity, granted []string) (capability.GrantSet, error) {
	if err := runtime.Validate(); err != nil {
		return nil, err
	}
	approval, err := VerifyInstallApproval(ctx, directory)
	if err != nil {
		return nil, err
	}
	if approval.ReviewDigest != launch.ReviewDigest || approval.Review.ID != runtime.OwnerID {
		return nil, fmt.Errorf("plugin owner or accepted review changed; reload required")
	}
	lifetime, revision, err := pluginGrantPolicy(launch.ReviewDigest)
	if err != nil {
		return nil, err
	}
	grants := capability.GrantSet{}
	seen := map[string]bool{}
	now := time.Now().UTC()
	for _, name := range granted {
		if seen[name] || !slices.Contains(launch.Granted, name) {
			return nil, fmt.Errorf("capability is not in the accepted launch")
		}
		seen[name] = true
		var raw json.RawMessage
		found := false
		for _, request := range approval.Review.Capabilities {
			if request.Name == name {
				raw = request.Metadata
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("capability is absent from the accepted review")
		}
		if len(raw) == 0 {
			raw = json.RawMessage(`{}`)
		}
		scope, err := reviewedGrantScope(name, raw)
		if err != nil {
			return nil, err
		}
		grants = append(grants, capability.Grant{GrantID: uuid.NewString(), Name: naniteGrantPrefix + name,
			SchemaVersion: NaniteGrantSchemaVersion, Scope: scope, HostInstance: runtime.HostInstance,
			OwnerID: runtime.OwnerID, OwnerGeneration: runtime.OwnerGeneration, Audience: "nanite",
			IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(lifetime).Format(time.RFC3339Nano), PolicyRevision: revision})
	}
	if err := grants.ValidateForRuntime(runtime); err != nil {
		return nil, err
	}
	return grants, nil
}

// RenewPluginGrants is host initiated. It rechecks the accepted bundle and
// current policy and can only extend the same authority in the same live
// incarnation. No public caller-supplied grants or user identity are accepted.
// The child must acknowledge replacement before this host commits; a published
// SDK without that port returns an explicit refusal.
func (h *Host) RenewPluginGrants(ctx context.Context, id string) (capability.GrantSet, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, ok := h.GetPlugin(id)
	if !ok {
		return nil, subprocess.ErrGrantLeaseEnded
	}
	child, ok := p.(*subprocess.SubprocessPlugin)
	if !ok {
		return nil, fmt.Errorf("plugin has no subprocess grant lease")
	}
	current := child.CurrentGrants()
	runtime := child.Incarnation()
	if runtime.Validate() != nil {
		return nil, subprocess.ErrGrantLeaseEnded
	}
	if err := CheckAcceptedBundle(ctx, child.Directory(), child.AcceptedReviewDigest()); err != nil {
		if ctx.Err() == nil {
			child.RevokeIncarnation(runtime)
		}
		return nil, err
	}
	lifetime, revision, err := pluginGrantPolicy(child.AcceptedReviewDigest())
	if err != nil {
		child.RevokeIncarnation(runtime)
		return nil, err
	}
	now := time.Now().UTC()
	for i := range current {
		if current[i].PolicyRevision != revision {
			child.RevokeIncarnation(runtime)
			return nil, fmt.Errorf("plugin policy changed; reload required")
		}
		current[i].IssuedAt, current[i].ExpiresAt = now.Format(time.RFC3339Nano), now.Add(lifetime).Format(time.RFC3339Nano)
	}
	if err := child.RenewGrants(ctx, current); err != nil {
		return nil, err
	}
	return current, nil
}

func revalidateReviewedGrants(ctx context.Context, directory, review string, grants capability.GrantSet) error {
	if err := CheckAcceptedBundle(ctx, directory, review); err != nil {
		return err
	}
	_, revision, err := pluginGrantPolicy(review)
	if err != nil {
		return err
	}
	for _, grant := range grants {
		if grant.PolicyRevision != revision {
			return fmt.Errorf("plugin grant policy changed; reload required")
		}
	}
	return nil
}
