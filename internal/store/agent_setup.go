package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/hollis-labs/nanite/internal/agentpolicy"

	mesh "github.com/hollis-labs/libs/mesh/core"
	"github.com/hollis-labs/libs/mesh/core/agentdef"
)

// ErrAgentSetupConflict reports that the slug is already taken by host
// settings that are not the ones this setup would create.
var ErrAgentSetupConflict = errors.New("host settings with this slug already exist with a different definition, provider, model, runtime or directories")

// AllAvailableTools asks AgentSetup to grant every available known tool.
const AllAvailableTools = "*"

// AgentSetup is one operator request to make a definition launchable as a
// native agent: install it, give it host settings, review those settings and
// grant tools, all or nothing.
type AgentSetup struct {
	Definition  []byte
	Resources   []DefinitionResource
	Slug        string // default: derived from the definition id
	Title       string // default: the definition name
	Provider    string
	Model       string
	Runtime     string   // "api" (default) or "cli"
	Directories []string // directories the agent may work in (host settings)
	Tools       []string // tool names, or AllAvailableTools
	GrantedBy   string   // accountable actor recorded on the authority
	Reason      string
}

// AgentSetupResult describes what exists after a setup, whether it was created
// by this call or already there.
type AgentSetupResult struct {
	DefinitionRef mesh.DefinitionRef
	Name          string
	HostSettings  AgentHostSettings
	AuthorityID   string
	Tools         []string // every tool the authority now grants, not just those requested
	Added         []string // tools this call granted for the first time
	Created       bool     // false when every part already existed
}

// AgentSetupSlug derives a host-settings slug from a definition id.
func AgentSetupSlug(definitionID string) string {
	var b strings.Builder
	dash := true
	for _, r := range strings.ToLower(definitionID) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// ResolveSetupTools turns requested tool names (dotted spellings allowed) into
// available known tool names, or AllAvailableTools into every available one.
// It reads only; callers use it to refuse before any write.
func (s *Store) ResolveSetupTools(ctx context.Context, requested []string) ([]string, error) {
	known, err := s.ListAvailableKnownTools(ctx)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	var all []string
	for _, k := range known {
		have[k.Name] = true
		all = append(all, k.Name)
	}
	var out, missing []string
	seen := map[string]bool{}
	for _, name := range requested {
		if name == AllAvailableTools {
			if len(all) == 0 {
				return nil, errors.New("no tools are available yet: start the server once so it records its tool catalog, then run setup again")
			}
			for _, n := range all {
				if !seen[n] {
					seen[n] = true
					out = append(out, n)
				}
			}
			continue
		}
		resolved := name
		if !have[resolved] {
			resolved = strings.ReplaceAll(name, ".", "_")
		}
		if !have[resolved] {
			missing = append(missing, name)
			continue
		}
		if !seen[resolved] {
			seen[resolved] = true
			out = append(out, resolved)
		}
	}
	if len(missing) > 0 {
		if len(all) == 0 {
			return nil, fmt.Errorf("unknown tools %s and no tools are recorded yet: start the server once so it records its tool catalog, then run setup again", strings.Join(missing, ", "))
		}
		return nil, fmt.Errorf("%w: %s; list grantable names with `authority tools`", ErrNativeToolNotGrantable, strings.Join(missing, ", "))
	}
	return out, nil
}

// SetupAgent installs the definition, creates (or finds) its host settings,
// reviews them and grants the tools in one transaction. Re-running with the
// same inputs changes nothing and reports the existing ids; a slug already
// bound to different settings is refused rather than silently reused.
func (s *Store) SetupAgent(ctx context.Context, in AgentSetup) (AgentSetupResult, error) {
	var res AgentSetupResult
	if strings.TrimSpace(in.GrantedBy) == "" {
		return res, ErrNativeAuthorityActor
	}
	di, err := prepareDefinitionInstall(in.Definition, in.Resources)
	if err != nil {
		return res, err
	}
	d, err := agentdef.Parse(in.Definition, agentpolicy.Option())
	if err != nil {
		return res, err
	}
	res.DefinitionRef, res.Name = di.pin, d.Name
	runtime := in.Runtime
	if runtime == "" {
		runtime = "api"
	}
	slug := in.Slug
	if slug == "" {
		slug = AgentSetupSlug(d.DefinitionID)
	}
	title := in.Title
	if title == "" {
		title = d.Name
	}
	if title == "" {
		title = slug
	}
	if err = ValidateHostSlug(slug); err != nil {
		return res, err
	}
	want := NativeHostSettings{Version: "1", Runtime: runtime, Provider: in.Provider, Model: in.Model, Directories: in.Directories}
	if err = want.Validate(); err != nil {
		return res, err
	}
	tools, err := s.ResolveSetupTools(ctx, in.Tools)
	if err != nil {
		return res, err
	}
	requestedTools := tools

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer rollbackUnlessCommitted(tx)
	if err = di.write(ctx, tx); err != nil {
		return res, err
	}
	created := false
	host, err := getAgentHostSettingsWhere(ctx, tx, "slug", slug)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		host, err = createAgentHostSettingsTx(ctx, tx, AgentHostSettings{Slug: slug, Title: title, DefinitionRef: di.pin, Settings: want, Enabled: true, Source: "operator"})
		if err != nil {
			return res, err
		}
		created = true
	case err != nil:
		return res, err
	default:
		if host.DefinitionRef != di.pin || !host.Enabled || host.Settings.Runtime != want.Runtime || host.Settings.Provider != want.Provider || host.Settings.Model != want.Model || !slices.Equal(host.Settings.Directories, want.Directories) {
			return res, fmt.Errorf("%w: slug %q (use --slug to choose another)", ErrAgentSetupConflict, slug)
		}
	}
	var authority NativeAuthority
	err = tx.QueryRowContext(ctx, `SELECT id,host_settings_id,host_settings_revision,definition_id,definition_revision,semantic_digest FROM native_authorities WHERE host_settings_id=? AND host_settings_revision=? AND enabled=1 AND revoked_at IS NULL`, host.ID, host.Revision).
		Scan(&authority.ID, &authority.HostSettingsID, &authority.HostSettingsRevision, &authority.DefinitionID, &authority.DefinitionRevision, &authority.SemanticDigest)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		authority, err = createAuthorityTx(ctx, tx, host, in.GrantedBy, in.Reason)
		if err != nil {
			return res, err
		}
		created = true
	case err != nil:
		return res, err
	}
	granted := func() (map[string]bool, error) {
		rows, qerr := tx.QueryContext(ctx, `SELECT k.name FROM native_authority_tools t JOIN known_tools k ON k.id=t.tool_id WHERE t.authority_id=? ORDER BY k.name`, authority.ID)
		if qerr != nil {
			return nil, qerr
		}
		defer func() { _ = rows.Close() }()
		out := map[string]bool{}
		for rows.Next() {
			var n string
			if qerr = rows.Scan(&n); qerr != nil {
				return nil, qerr
			}
			out[n] = true
		}
		return out, rows.Err()
	}
	have, err := granted()
	if err != nil {
		return res, err
	}
	for _, name := range requestedTools {
		if have[name] {
			continue
		}
		if err = grantToolTx(ctx, tx, authority, in.GrantedBy, authority.ID, name, in.Reason); err != nil {
			return res, err
		}
		res.Added = append(res.Added, name)
		created = true
	}
	if have, err = granted(); err != nil {
		return res, err
	}
	for name := range have {
		res.Tools = append(res.Tools, name)
	}
	sort.Strings(res.Tools)
	if err = tx.Commit(); err != nil {
		return res, err
	}
	res.HostSettings, res.AuthorityID, res.Created = host, authority.ID, created
	return res, nil
}
