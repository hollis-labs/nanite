package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	mesh "github.com/hollis-labs/libs/mesh/core"
	"github.com/hollis-labs/libs/mesh/core/agentdef"
	"github.com/hollis-labs/nanite/internal/agentpolicy"
)

// ErrAgentLoadPinned is returned when a pinned agent would get another
// revision without being forced.
var ErrAgentLoadPinned = errors.New("agent is pinned: it already has a loaded revision")

// AgentLoadSource is the host-settings source recorded for a loaded revision;
// the full value names the agent so its revisions can be found together.
const AgentLoadSource = "directory:"

// AgentLoad publishes one state of an agent directory.
type AgentLoad struct {
	Definition []byte
	Resources  []DefinitionResource
	// Agent names the agent across revisions (the slug prefix).
	Agent    string
	Title    string
	Settings NativeHostSettings
	// Pinned refuses to add a revision when the agent already has one, unless Force.
	Pinned bool
	Force  bool
	// GrantFrom, when set, names the host settings (id or slug) whose current tool
	// grants are re-granted to the new revision. Nothing else carries forward.
	GrantFrom string
	GrantedBy string
	Reason    string
	DryRun    bool
}

// AgentLoadResult describes the state after (or, for a dry run, if) the load.
type AgentLoadResult struct {
	DefinitionRef mesh.DefinitionRef
	HostSettings  AgentHostSettings
	// Previous is the agent's most recent earlier revision, if any.
	Previous *AgentHostSettings
	// Created reports that this call wrote something; HostCreated that the host row is new.
	Created     bool
	HostCreated bool
	AuthorityID string
	Tools       []string
	Added       []string
}

// LoadAgent installs the definition and records a host-settings row for this
// state of the directory, in one transaction. The row is named from the agent
// and a hash of the definition digest and the host settings, so loading the
// same state again changes nothing and an edit never touches the row an older
// session was admitted under. The new row has no authority: tools are granted
// only when GrantFrom names an explicit source.
func (s *Store) LoadAgent(ctx context.Context, in AgentLoad) (AgentLoadResult, error) {
	var res AgentLoadResult
	di, err := prepareDefinitionInstall(in.Definition, in.Resources)
	if err != nil {
		return res, badSetupRequest(err)
	}
	d, err := agentdef.Parse(in.Definition, agentpolicy.Option())
	if err != nil {
		return res, badSetupRequest(err)
	}
	if err = in.Settings.Validate(); err != nil {
		return res, badSetupRequest(err)
	}
	agent := in.Agent
	if agent == "" {
		agent = AgentSetupSlug(d.DefinitionID)
	}
	if err = ValidateHostSlug(agent); err != nil {
		return res, badSetupRequest(err)
	}
	settingsJSON, err := json.Marshal(in.Settings)
	if err != nil {
		return res, err
	}
	sum := sha256.Sum256([]byte(di.pin.ID + "\n" + di.pin.Revision + "\n" + di.pin.Digest + "\n" + string(settingsJSON)))
	slug := agent + "-" + hex.EncodeToString(sum[:])[:8]
	if err = ValidateHostSlug(slug); err != nil {
		return res, badSetupRequest(err)
	}
	title := in.Title
	if title == "" {
		title = d.Name
	}
	if title == "" {
		title = agent
	}
	res.DefinitionRef = di.pin

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer rollbackUnlessCommitted(tx)

	source := AgentLoadSource + agent
	var prevID string
	switch qerr := tx.QueryRowContext(ctx, `SELECT id FROM agent_host_settings WHERE source=? AND slug<>? ORDER BY rowid DESC LIMIT 1`, source, slug).Scan(&prevID); {
	case errors.Is(qerr, sql.ErrNoRows):
	case qerr != nil:
		return res, qerr
	default:
		prev, getErr := getAgentHostSettingsWhere(ctx, tx, "id", prevID)
		if getErr != nil {
			return res, getErr
		}
		res.Previous = &prev
	}

	if err = di.write(ctx, tx); err != nil {
		return res, err
	}
	host, err := getAgentHostSettingsWhere(ctx, tx, "slug", slug)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if in.Pinned && !in.Force && res.Previous != nil {
			return res, fmt.Errorf("%w (%s); pass force to publish a new revision", ErrAgentLoadPinned, res.Previous.Slug)
		}
		host, err = createAgentHostSettingsTx(ctx, tx, AgentHostSettings{Slug: slug, Title: title, DefinitionRef: di.pin, Settings: in.Settings, Enabled: true, Source: source})
		if err != nil {
			return res, err
		}
		res.Created, res.HostCreated = true, true
	case err != nil:
		return res, err
	default:
		// The slug is a hash, but an existing row is reused only when it is
		// exactly this state.
		if host.DefinitionRef != di.pin || host.Source != source {
			return res, fmt.Errorf("host settings %s exist for a different definition pin or source; refusing to reuse them", slug)
		}
		// The authored state is the definition pin and the host settings
		// (runtime, provider, model, directories, ...). Title and enabled are
		// operator-owned and not compared: a retitled or disabled row is still
		// this state, and a load neither renames nor re-enables it. A settings
		// mismatch is refused, never rewritten, because older sessions may be
		// running under the row.
		if diff := differingSettings(host.Settings, in.Settings); len(diff) > 0 {
			return res, fmt.Errorf("host settings %s (%s) were edited after they were loaded and differ from host.yaml in: %s; change host.yaml (a new state loads as a new row) or restore the row", host.Slug, host.ID, strings.Join(diff, ", "))
		}
	}
	res.HostSettings = host

	if in.GrantFrom != "" {
		if strings.TrimSpace(in.GrantedBy) == "" {
			return res, ErrNativeAuthorityActor
		}
		from, getErr := getAgentHostSettingsWhere(ctx, tx, "id", in.GrantFrom)
		if errors.Is(getErr, sql.ErrNoRows) {
			from, getErr = getAgentHostSettingsWhere(ctx, tx, "slug", in.GrantFrom)
		}
		if getErr != nil {
			return res, fmt.Errorf("grant source %q: %w", in.GrantFrom, getErr)
		}
		names, namesErr := authorityToolNames(ctx, tx, from.ID, from.Revision)
		if namesErr != nil {
			return res, namesErr
		}
		var authority NativeAuthority
		qerr := tx.QueryRowContext(ctx, `SELECT id,host_settings_id,host_settings_revision,definition_id,definition_revision,semantic_digest FROM native_authorities WHERE host_settings_id=? AND host_settings_revision=? AND enabled=1 AND revoked_at IS NULL`, host.ID, host.Revision).
			Scan(&authority.ID, &authority.HostSettingsID, &authority.HostSettingsRevision, &authority.DefinitionID, &authority.DefinitionRevision, &authority.SemanticDigest)
		switch {
		case errors.Is(qerr, sql.ErrNoRows):
			if authority, err = createAuthorityTx(ctx, tx, host, in.GrantedBy, in.Reason); err != nil {
				return res, err
			}
			res.Created = true
		case qerr != nil:
			return res, qerr
		}
		have, haveErr := authorityToolNames(ctx, tx, host.ID, host.Revision)
		if haveErr != nil {
			return res, haveErr
		}
		for _, name := range names {
			if containsString(have, name) {
				continue
			}
			if err = grantToolTx(ctx, tx, authority, in.GrantedBy, authority.ID, name, in.Reason); err != nil {
				return res, err
			}
			res.Added = append(res.Added, name)
		}
		res.AuthorityID = authority.ID
		if res.Tools, err = authorityToolNames(ctx, tx, host.ID, host.Revision); err != nil {
			return res, err
		}
	}
	if in.DryRun {
		return res, nil // the deferred rollback discards everything above
	}
	return res, tx.Commit()
}

func authorityToolNames(ctx context.Context, tx *sql.Tx, hostID, revision string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT k.name FROM native_authorities a JOIN native_authority_tools t ON t.authority_id=a.id JOIN known_tools k ON k.id=t.tool_id WHERE a.host_settings_id=? AND a.host_settings_revision=? AND a.enabled=1 AND a.revoked_at IS NULL ORDER BY k.name`, hostID, revision)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err = rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out, rows.Err()
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// differingSettings names the top-level settings fields whose canonical JSON differs.
func differingSettings(stored, authored NativeHostSettings) []string {
	toMap := func(v NativeHostSettings) map[string]json.RawMessage {
		raw, _ := json.Marshal(v)
		m := map[string]json.RawMessage{}
		_ = json.Unmarshal(raw, &m)
		return m
	}
	a, b := toMap(stored), toMap(authored)
	var out []string
	for k, v := range a {
		if string(v) != string(b[k]) {
			out = append(out, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
