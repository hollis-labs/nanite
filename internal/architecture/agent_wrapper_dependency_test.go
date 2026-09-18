package architecture_test

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

type agentOwnershipSymbol struct {
	path       string
	importPath string
	symbol     string
}

type agentOwnershipAllowance struct {
	count     int
	rationale string
}

func ownershipSymbol(path, importPath, symbol string) agentOwnershipSymbol {
	return agentOwnershipSymbol{path: path, importPath: importPath, symbol: symbol}
}

func ownershipAllowance(count int, rationale string) agentOwnershipAllowance {
	return agentOwnershipAllowance{count: count, rationale: rationale}
}

var allowedAgentOwnershipSymbols = map[agentOwnershipSymbol]agentOwnershipAllowance{
	// Boot constructs exactly one wrapper and passes only wrapper-owned launch
	// DTOs. It never constructs the child runtime or protocol client itself.
	ownershipSymbol("internal/runtime/agent/agent.go", agentWrapperModule+"/wrapper", "Wrapper"):            ownershipAllowance(1, "Nanite stores the wrapper handle returned by Boot"),
	ownershipSymbol("internal/runtime/agent/agent.go", agentWrapperModule+"/wrapper", "New"):                ownershipAllowance(1, "Boot's sole wrapper construction boundary"),
	ownershipSymbol("internal/runtime/agent/agent.go", agentWrapperModule+"/wrapper", "Config"):             ownershipAllowance(1, "Nanite compiles wrapper launch inputs"),
	ownershipSymbol("internal/runtime/agent/agent.go", agentWrapperModule+"/wrapper", "ChildEnvironment"):   ownershipAllowance(1, "Nanite supplies the isolated child environment DTO"),
	ownershipSymbol("internal/runtime/agent/agent.go", agentWrapperModule+"/wrapper", "EnvironmentReplace"): ownershipAllowance(1, "Nanite selects replacement environment semantics"),
	ownershipSymbol("internal/runtime/agent/agent.go", agentWrapperModule+"/adapters", "Adapter"):           ownershipAllowance(1, "Boot passes the selected adapter into wrapper.Config"),
	ownershipSymbol("internal/runtime/agent/agent.go", agentWrapperModule+"/activity", "NewBridge"):         ownershipAllowance(1, "Nanite injects its normalized event sink"),

	// Session is a thin wrapper facade plus recovery translation; lifecycle
	// calls stay on wrapper.Wrapper and ACP/agentkit types stay diagnostic-only.
	ownershipSymbol("internal/runtime/agent/manager.go", agentWrapperModule+"/wrapper", "ErrTurnCancelUnsupported"):    ownershipAllowance(1, "Nanite re-exports wrapper's honest native cancel ceiling"),
	ownershipSymbol("internal/runtime/agent/manager.go", agentWrapperModule+"/acp", "LifecycleError"):                  ownershipAllowance(1, "Recovery preserves wrapper's classified ACP error"),
	ownershipSymbol("internal/runtime/agent/manager.go", agentWrapperModule+"/acp", "StateProcessing"):                 ownershipAllowance(1, "Takeover waits on wrapper-reported ACP state"),
	ownershipSymbol("internal/runtime/agent/manager.go", "github.com/hollis-labs/agentkit/agentsessions", "ExitError"): ownershipAllowance(2, "Legacy recovery error translation only; no agentkit session ownership"),

	// SessionManager owns only Nanite runtime-ID bindings. The one shared ACP
	// manager is injected into every wrapper; wrapper sessions remain its data.
	ownershipSymbol("internal/runtime/agent/session_manager.go", agentWrapperModule+"/acp", "Manager"):    ownershipAllowance(2, "Field and accessor for the one shared wrapper ACP manager"),
	ownershipSymbol("internal/runtime/agent/session_manager.go", agentWrapperModule+"/acp", "NewManager"): ownershipAllowance(1, "Construct the one manager shared by all wrapper sessions"),

	// The approval bridge translates only Nanite vocabulary and provider-offered
	// DTOs. It neither constructs a client nor controls a session lifecycle.
	ownershipSymbol("internal/runtime/agent/approval_bridge.go", agentWrapperModule+"/acp", "BestEffortPermissionRequestResponder"): ownershipAllowance(1, "Inject Nanite's existing approval bridge"),
	ownershipSymbol("internal/runtime/agent/approval_bridge.go", agentWrapperModule+"/acp", "PermissionRequest"):                    ownershipAllowance(1, "Read the provider's permission DTO"),
	ownershipSymbol("internal/runtime/agent/approval_bridge.go", agentWrapperModule+"/acp", "PermissionSelection"):                  ownershipAllowance(5, "Return only provider-offered selections or zero"),
	ownershipSymbol("internal/runtime/agent/approval_bridge.go", agentWrapperModule+"/acp", "PermissionOption"):                     ownershipAllowance(1, "Inspect provider-offered options"),
	ownershipSymbol("internal/runtime/agent/approval_bridge.go", agentWrapperModule+"/acp", "PermissionOptionKind"):                 ownershipAllowance(5, "Map Nanite scope onto exact option kinds"),
	ownershipSymbol("internal/runtime/agent/approval_bridge.go", agentWrapperModule+"/acp", "PermissionAllowAlways"):                ownershipAllowance(1, "Session-scoped allow preference"),
	ownershipSymbol("internal/runtime/agent/approval_bridge.go", agentWrapperModule+"/acp", "PermissionAllowOnce"):                  ownershipAllowance(2, "Safe one-shot allow and fallback"),
	ownershipSymbol("internal/runtime/agent/approval_bridge.go", agentWrapperModule+"/acp", "PermissionRejectAlways"):               ownershipAllowance(1, "Session-scoped reject preference"),
	ownershipSymbol("internal/runtime/agent/approval_bridge.go", agentWrapperModule+"/acp", "PermissionRejectOnce"):                 ownershipAllowance(2, "Safe one-shot reject and fallback"),
	ownershipSymbol("internal/runtime/agent/approval_bridge.go", agentWrapperModule+"/acp", "SelectPermissionOption"):               ownershipAllowance(1, "Return an exact provider option ID"),

	// Dependency and selection DTOs are Nanite composition inputs.
	ownershipSymbol("internal/runtime/agent/deps.go", agentWrapperModule+"/adapters", "Transport"):                              ownershipAllowance(1, "ACP adapter factory input DTO"),
	ownershipSymbol("internal/runtime/agent/deps.go", agentWrapperModule+"/adapters", "Adapter"):                                ownershipAllowance(1, "ACP adapter factory output interface"),
	ownershipSymbol("internal/runtime/agent/deps.go", "github.com/hollis-labs/agentkit/agentsessions", "ExitError"):             ownershipAllowance(4, "Recovery and telemetry compatibility DTO only"),
	ownershipSymbol("internal/runtime/agent/acp_adapter.go", agentWrapperModule+"/adapters", "Transport"):                       ownershipAllowance(1, "Pass the configured ACP transport to shipped adapters"),
	ownershipSymbol("internal/runtime/agent/acp_adapter.go", agentWrapperModule+"/adapters", "Adapter"):                         ownershipAllowance(1, "Return an adapter for wrapper ownership"),
	ownershipSymbol("internal/runtime/agent/acp_adapter.go", agentWrapperModule+"/adapters/claudeacp", "New"):                   ownershipAllowance(1, "Construct the shipped Claude adapter, not its client"),
	ownershipSymbol("internal/runtime/agent/acp_adapter.go", agentWrapperModule+"/adapters/codexacp", "New"):                    ownershipAllowance(1, "Construct the shipped Codex adapter, not its client"),
	ownershipSymbol("internal/runtime/agent/acp_adapter.go", agentWrapperModule+"/adapters/copilotacp", "New"):                  ownershipAllowance(1, "Construct the shipped Copilot adapter, not its client"),
	ownershipSymbol("internal/runtime/agent/acp_adapter.go", agentWrapperModule+"/adapters/copilotacp", "WithAdapterTransport"): ownershipAllowance(1, "Select Copilot's configured wrapper transport"),
	ownershipSymbol("internal/runtime/agent/acp_adapter.go", agentWrapperModule+"/adapters/opencodeacp", "New"):                 ownershipAllowance(1, "Construct the shipped OpenCode adapter, not its client"),
	ownershipSymbol("internal/runtime/agent/acp_adapter.go", agentWrapperModule+"/adapters/piacp", "New"):                       ownershipAllowance(1, "Construct the shipped Pi adapter, not its client"),
	ownershipSymbol("internal/runtime/agent/factory.go", agentWrapperModule+"/adapters", "RuntimeAdapter"):                      ownershipAllowance(1, "Return wrapper's selected native adapter"),
	ownershipSymbol("internal/runtime/agent/factory.go", agentWrapperModule+"/adapters", "LaunchSubprocessPerTurn"):             ownershipAllowance(1, "Nanite's Codex/OpenCode launch-mode policy"),
	ownershipSymbol("internal/runtime/agent/factory.go", agentWrapperModule+"/adapters", "LaunchStreamingStdio"):                ownershipAllowance(1, "Nanite's Claude launch-mode policy"),
	ownershipSymbol("internal/runtime/agent/factory.go", agentWrapperModule+"/adapters", "Select"):                              ownershipAllowance(1, "Canonical wrapper native adapter selection"),
	ownershipSymbol("internal/runtime/agent/factory.go", agentWrapperModule+"/adapters", "Selection"):                           ownershipAllowance(1, "Nanite compiles the adapter selection DTO"),
	ownershipSymbol("internal/runtime/agent/factory.go", agentWrapperModule+"/adapters", "Provider"):                            ownershipAllowance(1, "Normalize Nanite provider identity into wrapper vocabulary"),
	ownershipSymbol("internal/runtime/agent/factory.go", agentWrapperModule+"/adapters", "RuntimeKindCLI"):                      ownershipAllowance(1, "This package handles CLI runtimes only"),
	ownershipSymbol("internal/runtime/agent/factory.go", agentWrapperModule+"/adapters", "Transport"):                           ownershipAllowance(1, "Return the configured ACP transport DTO"),
	ownershipSymbol("internal/runtime/agent/factory.go", agentWrapperModule+"/adapters", "TransportTCP"):                        ownershipAllowance(1, "Copilot TCP profile selection"),
	ownershipSymbol("internal/runtime/agent/factory.go", agentWrapperModule+"/adapters", "TransportStdio"):                      ownershipAllowance(1, "Default ACP transport selection"),

	// Boot-dir planting is Nanite-owned content compilation expressed only in
	// wrapper's destination-agnostic DTOs; no process/session control is exposed.
	//
	// CW-20260910-0020 revised these counts. Two shapes changed, neither of
	// which moves the ownership line this test defends:
	//
	//  1. Layout.Populate now RETURNS plant.Result so the shared
	//     materialization Handle (manifest, ownership, per-entry change
	//     data) reaches its caller instead of being dropped at the planting
	//     boundary. That raises plant.Result in bootdir.go and in each
	//     provider layout. Result is a reporting DTO — it carries what was
	//     written, not a handle to a process or an ACP session.
	// CW-20260910-0015 raised plant.Spec once per provider: the hook guard
	// and the two unwired-provider guards each return an empty plant.Spec
	// alongside their error. Still DTO construction — the hook mechanism
	// itself deliberately does NOT route through plant.Spec.Hooks (see
	// bootdir_plant.go's plantSpec).
	//  2. plantSpec delegates the WRITE to plant.SharedPlanter, which routes
	//     the artifact tree through agentkit's shared materialization
	//     engine. SharedPlanter is a file-writing helper over
	//     agentlaunch.MaterializeArtifacts; it has no process, transport or
	//     session surface, and Nanite still decides every destination path
	//     and mode itself (bootDirArtifactTree) rather than accepting
	//     wrapper's legacy per-field conventions.
	ownershipSymbol("internal/runtime/agent/bootdir.go", agentWrapperModule+"/plant", "Result"):              ownershipAllowance(3, "Layout.Populate surfaces the materialization result"),
	ownershipSymbol("internal/runtime/agent/bootdir_claude.go", agentWrapperModule+"/plant", "Planter"):      ownershipAllowance(1, "Claude boot-content planter conformance"),
	ownershipSymbol("internal/runtime/agent/bootdir_claude.go", agentWrapperModule+"/plant", "Spec"):         ownershipAllowance(9, "Compile Claude boot-content DTOs"),
	ownershipSymbol("internal/runtime/agent/bootdir_claude.go", agentWrapperModule+"/plant", "Result"):       ownershipAllowance(4, "Return planted-content metadata"),
	ownershipSymbol("internal/runtime/agent/bootdir_codex.go", agentWrapperModule+"/plant", "Planter"):       ownershipAllowance(1, "Codex boot-content planter conformance"),
	ownershipSymbol("internal/runtime/agent/bootdir_codex.go", agentWrapperModule+"/plant", "Spec"):          ownershipAllowance(8, "Compile Codex boot-content DTOs"),
	ownershipSymbol("internal/runtime/agent/bootdir_codex.go", agentWrapperModule+"/plant", "Result"):        ownershipAllowance(4, "Return planted-content metadata"),
	ownershipSymbol("internal/runtime/agent/bootdir_opencode.go", agentWrapperModule+"/plant", "Planter"):    ownershipAllowance(1, "OpenCode boot-content planter conformance"),
	ownershipSymbol("internal/runtime/agent/bootdir_opencode.go", agentWrapperModule+"/plant", "Spec"):       ownershipAllowance(9, "Compile OpenCode boot-content DTOs"),
	ownershipSymbol("internal/runtime/agent/bootdir_opencode.go", agentWrapperModule+"/plant", "Result"):     ownershipAllowance(4, "Return planted-content metadata"),
	ownershipSymbol("internal/runtime/agent/bootdir_plant.go", agentWrapperModule+"/plant", "Spec"):          ownershipAllowance(3, "Consume the destination-agnostic planting DTO"),
	ownershipSymbol("internal/runtime/agent/bootdir_plant.go", agentWrapperModule+"/plant", "Result"):        ownershipAllowance(5, "Return exact planting outcomes"),
	ownershipSymbol("internal/runtime/agent/bootdir_plant.go", agentWrapperModule+"/plant", "SharedPlanter"): ownershipAllowance(1, "Write through agentkit's shared materialization engine"),
	ownershipSymbol("internal/runtime/agent/skill_plant.go", agentWrapperModule+"/plant", "Spec"):            ownershipAllowance(1, "Plant mid-session skill grants through the same engine-owned path"),
}

const (
	agentWrapperModule    = "github.com/hollis-labs/go-agent-wrapper"
	agentWrapperVersion   = "v0.10.2-0.20260918211027-507ed1cef628"
	agentWrapperSum       = "h1:Yy+iZbNcRZuha2Xa+YIAQ214iT9Eg7AsFDUhDiFaOCA="
	agentWrapperGoModSum  = "h1:F9XVYz7c8rwvw7mc0L8pWNYGE06WYSHqMSQB7T1mCEE="
	runtimeEventsModule   = "github.com/hollis-labs/go-runtime-events"
	runtimeEventsVersion  = "v0.1.2"
	runtimeEventsSum      = "h1:ChyjCVidjeAVf+dUJjiJTdX/LYujkJILKuLi+cTDNJM="
	runtimeEventsGoModSum = "h1:QLoJ1xs+P2KoVuiz4wi+r+JZdE4dqm28I0YfauXAUs0="
)

func TestAgentWrapperDependencyBoundary(t *testing.T) {
	root := repositoryRoot(t)
	repository, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	repositoryFS := repository.FS()

	moduleData, err := fs.ReadFile(repositoryFS, "go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if violations := validateAgentWrapperModules(moduleData); len(violations) != 0 {
		t.Fatalf("agent-wrapper module boundary failed:\n- %s", strings.Join(violations, "\n- "))
	}
	sumData, err := fs.ReadFile(repositoryFS, "go.sum")
	if err != nil {
		t.Fatal(err)
	}
	if violations := validateAgentWrapperSums(sumData); len(violations) != 0 {
		t.Fatalf("agent-wrapper checksum boundary failed:\n- %s", strings.Join(violations, "\n- "))
	}
	if violations, err := scanRuntimeAgentOwnership(repositoryFS); err != nil {
		t.Fatal(err)
	} else if len(violations) != 0 {
		t.Fatalf("runtime agent bypasses wrapper lifecycle ownership:\n- %s", strings.Join(violations, "\n- "))
	}
}

func TestAgentWrapperDependencyValidatorsRejectMutants(t *testing.T) {
	if violations := validateAgentWrapperModules([]byte("module example.com/host\nrequire " + agentWrapperModule + " v0.8.1\nrequire " + runtimeEventsModule + " " + runtimeEventsVersion + "\n")); len(violations) == 0 {
		t.Fatal("wrong wrapper module version was accepted")
	}
	if violations := validateAgentWrapperModules([]byte("module example.com/host\nrequire " + agentWrapperModule + " " + agentWrapperVersion + "\nrequire " + runtimeEventsModule + " " + runtimeEventsVersion + "\nreplace " + agentWrapperModule + " => ../wrapper\n")); len(violations) == 0 {
		t.Fatal("local wrapper replace was accepted")
	}
}

func TestAgentOwnershipGuardRejectsPlantedProductionViolations(t *testing.T) {
	tests := map[string]struct {
		source string
		want   []string
	}{
		"exec alias Command": {
			source: `package agent
import runner "os/exec"
func start() { _ = runner.Command("agent") }`,
			want: []string{"direct os/exec import", "Command"},
		},
		"exec alias CommandContext": {
			source: `package agent
import (
    "context"
    process "os/exec"
)
func start(ctx context.Context) { _ = process.CommandContext(ctx, "agent") }`,
			want: []string{"direct os/exec import", "CommandContext"},
		},
		"direct ACP constructor and lifecycle": {
			source: `package agent
import (
    "context"
    protocol "github.com/hollis-labs/go-agent-wrapper/acp"
)
func start(ctx context.Context) {
    client := protocol.NewClient()
    _ = client.Launch(ctx, protocol.LaunchParams{})
    _ = client.Prompt(ctx, "hello")
    _ = client.Close(ctx)
}`,
			want: []string{"acp.NewClient", "direct ACP client lifecycle Launch", "direct ACP client lifecycle Prompt", "direct ACP client lifecycle Close"},
		},
		"typed ACP client parameter lifecycle": {
			source: `package agent
import (
    "context"
    protocol "github.com/hollis-labs/go-agent-wrapper/acp"
)
func prompt(ctx context.Context, client protocol.Client) {
    _ = client.Prompt(ctx, "hello")
}`,
			want: []string{"acp.Client", "direct ACP client lifecycle Prompt"},
		},
		"short declaration ACP client alias": {
			source: `package agent
import (
    "context"
    protocol "github.com/hollis-labs/go-agent-wrapper/acp"
)
func prompt(ctx context.Context) {
    client := protocol.NewClient()
    alias := client
    _ = alias.Prompt(ctx, "hello")
}`,
			want: []string{"acp.NewClient", "direct ACP client lifecycle Prompt"},
		},
		"var ACP client alias": {
			source: `package agent
import (
    "context"
    protocol "github.com/hollis-labs/go-agent-wrapper/acp"
)
func prompt(ctx context.Context, client protocol.Client) {
    var alias = client
    _ = alias.Prompt(ctx, "hello")
}`,
			want: []string{"acp.Client", "direct ACP client lifecycle Prompt"},
		},
		"later assignment ACP client alias": {
			source: `package agent
import (
    "context"
    protocol "github.com/hollis-labs/go-agent-wrapper/acp"
)
func cancel(ctx context.Context, client protocol.Client) {
    var alias any
    alias = client
    _ = alias.Cancel(ctx)
}`,
			want: []string{"acp.Client", "direct ACP client lifecycle Cancel"},
		},
		"known holder field ACP client alias": {
			source: `package agent
import (
    "context"
    protocol "github.com/hollis-labs/go-agent-wrapper/acp"
)
type holder struct { client protocol.Client }
func closeClient(ctx context.Context, value *holder) {
    alias := value.client
    _ = alias.Close(ctx)
}`,
			want: []string{"acp.Client", "direct ACP client lifecycle Close"},
		},
		"multi-hop ACP client alias": {
			source: `package agent
import (
    "context"
    protocol "github.com/hollis-labs/go-agent-wrapper/acp"
)
var client protocol.Client
var third = second
var second = first
var first = client
func prompt(ctx context.Context) {
    _ = third.Prompt(ctx, "hello")
}`,
			want: []string{"acp.Client", "direct ACP client lifecycle Prompt"},
		},
		"direct adapter client": {
			source: `package agent
import (
    "context"
    bridge "github.com/hollis-labs/go-agent-wrapper/adapters/claudeacp"
)
func start(ctx context.Context) {
    client := bridge.NewClient()
    _ = client.Prompt(ctx, "hello")
}`,
			want: []string{"claudeacp.NewClient", "direct ACP client lifecycle Prompt"},
		},
		"direct agentkit manager": {
			source: `package agent
import sessions "github.com/hollis-labs/agentkit/agentsessions"
var manager = sessions.NewManager(nil)`,
			want: []string{"agentsessions.NewManager"},
		},
		"blank ACP import": {
			source: `package agent
import _ "github.com/hollis-labs/go-agent-wrapper/acp"`,
			want: []string{"blank import has no reviewed Agent Host symbol use"},
		},
		"dot os StartProcess": {
			source: `package agent
import . "os"
func start() { _, _ = StartProcess("agent", nil, nil) }`,
			want: []string{"dot import can bypass the Agent Host ownership guard"},
		},
		"retired local ownership": {
			source: `package agent
type envWrappedCLIAdapter struct{}
func writeEnvWrapperScript() {}`,
			want: []string{"retired local lifecycle declaration envWrappedCLIAdapter", "retired local lifecycle declaration writeEnvWrapperScript"},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			productionPath := "internal/runtime/agent/violation.go"
			files := fstest.MapFS{productionPath: {Data: []byte(test.source)}}
			violations, err := scanAgentOwnership(files, "internal/runtime/agent", nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range test.want {
				if !containsViolation(violations, want) {
					t.Fatalf("missing actionable violation %q in:\n- %s", want, strings.Join(violations, "\n- "))
				}
			}
		})
	}
}

func TestAgentOwnershipGuardDoesNotTaintUnrelatedIdentifiers(t *testing.T) {
	const productionPath = "internal/runtime/agent/unrelated.go"
	files := fstest.MapFS{productionPath: {Data: []byte(`package agent
import (
    "context"
    protocol "github.com/hollis-labs/go-agent-wrapper/acp"
)
type holder struct { client protocol.Client }
type unrelated struct { client string }
func retain(client protocol.Client) {}
func use(ctx context.Context, client unrelated) {
    alias := client.client
    _ = alias.Prompt(ctx, "not an ACP client")
}`)}}
	violations, err := scanAgentOwnership(files, "internal/runtime/agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if containsViolation(violations, "direct ACP client lifecycle Prompt") {
		t.Fatalf("unrelated identifiers inherited ACP client taint:\n- %s", strings.Join(violations, "\n- "))
	}
}

func TestAgentOwnershipGuardDoesNotMergeShadowedHolderTypes(t *testing.T) {
	const productionPath = "internal/runtime/agent/shadowed.go"
	files := fstest.MapFS{productionPath: {Data: []byte(`package agent
import (
    "context"
    protocol "github.com/hollis-labs/go-agent-wrapper/acp"
)
type promptLike struct{}
func (promptLike) Prompt(context.Context, string) error { return nil }
func forbidden(seed protocol.Client) {
    type holder struct { client protocol.Client }
    value := holder{client: seed}
    _ = value
}
func unrelated(ctx context.Context) {
    type holder struct { client promptLike }
    value := holder{}
    alias := value.client
    _ = alias.Prompt(ctx, "not an ACP client")
}`)}}
	violations, err := scanAgentOwnership(files, "internal/runtime/agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if containsViolation(violations, "direct ACP client lifecycle Prompt") {
		t.Fatalf("shadowed holder declaration inherited another type's field taint:\n- %s", strings.Join(violations, "\n- "))
	}
}

func TestAgentOwnershipGuardRejectsAllowanceOveruse(t *testing.T) {
	const productionPath = "internal/runtime/agent/agent.go"
	symbol := ownershipSymbol(productionPath, agentWrapperModule+"/wrapper", "New")
	allowed := map[agentOwnershipSymbol]agentOwnershipAllowance{
		symbol: ownershipAllowance(1, "test allowance must remain exact"),
	}
	files := fstest.MapFS{productionPath: {Data: []byte(`package agent
import host "github.com/hollis-labs/go-agent-wrapper/wrapper"
var first = host.New
var second = host.New`)}}
	violations, err := scanAgentOwnership(files, "internal/runtime/agent", allowed)
	if err != nil {
		t.Fatal(err)
	}
	if !containsViolation(violations, "expected github.com/hollis-labs/go-agent-wrapper/wrapper.New exactly 1 time(s), found 2") {
		t.Fatalf("exact allowance silently broadened:\n- %s", strings.Join(violations, "\n- "))
	}
}

func TestAgentOwnershipGuardIgnoresCommentsStringsAndTestFixtures(t *testing.T) {
	files := fstest.MapFS{
		"internal/runtime/agent/safe.go": {Data: []byte(`package agent
// exec.CommandContext and acp.NewClient are documentation, not ownership.
const example = "#!/bin/sh; exec env -i; acp.NewClient().Prompt()"`)},
		"internal/runtime/agent/process_fixture_test.go": {Data: []byte(`package agent
import process "os/exec"
func fixture() { _ = process.CommandContext(nil, "fake-agent") }`)},
	}
	violations, err := scanAgentOwnership(files, "internal/runtime/agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("comments, strings, or test fixtures produced violations:\n- %s", strings.Join(violations, "\n- "))
	}
}

func TestAgentOwnershipAllowancesAreExactAndAnchored(t *testing.T) {
	for symbol, allowance := range allowedAgentOwnershipSymbols {
		if symbol.path == "" || symbol.importPath == "" || symbol.symbol == "" || allowance.count <= 0 || strings.TrimSpace(allowance.rationale) == "" {
			t.Fatalf("invalid ownership allowance: %#v => %#v", symbol, allowance)
		}
	}
	anchors := []agentOwnershipSymbol{
		ownershipSymbol("internal/runtime/agent/agent.go", agentWrapperModule+"/wrapper", "New"),
		ownershipSymbol("internal/runtime/agent/factory.go", agentWrapperModule+"/adapters", "Select"),
		ownershipSymbol("internal/runtime/agent/acp_adapter.go", agentWrapperModule+"/adapters/claudeacp", "New"),
		ownershipSymbol("internal/runtime/agent/acp_adapter.go", agentWrapperModule+"/adapters/codexacp", "New"),
		ownershipSymbol("internal/runtime/agent/acp_adapter.go", agentWrapperModule+"/adapters/copilotacp", "New"),
		ownershipSymbol("internal/runtime/agent/acp_adapter.go", agentWrapperModule+"/adapters/opencodeacp", "New"),
		ownershipSymbol("internal/runtime/agent/acp_adapter.go", agentWrapperModule+"/adapters/piacp", "New"),
	}
	for _, anchor := range anchors {
		if allowance, ok := allowedAgentOwnershipSymbols[anchor]; !ok || allowance.count != 1 {
			t.Fatalf("missing exact positive anchor for %#v", anchor)
		}
	}
}

func containsViolation(violations []string, fragment string) bool {
	for _, violation := range violations {
		if strings.Contains(violation, fragment) {
			return true
		}
	}
	return false
}

func validateAgentWrapperModules(data []byte) []string {
	want := map[string]string{agentWrapperModule: agentWrapperVersion, runtimeEventsModule: runtimeEventsVersion}
	found := make(map[string]int)
	var violations []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if comment := strings.Index(line, "//"); comment >= 0 {
			line = line[:comment]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "replace" || strings.Contains(line, "=>") {
			for module := range want {
				if strings.Contains(line, module) {
					violations = append(violations, "go.mod must not replace "+module)
				}
			}
		}
		for i, field := range fields {
			expected, ok := want[field]
			if !ok {
				continue
			}
			found[field]++
			version := "<missing>"
			if i+1 < len(fields) {
				version = fields[i+1]
			}
			if version != expected {
				violations = append(violations, fmt.Sprintf("%s version = %s, want %s", field, version, expected))
			}
		}
	}
	for module := range want {
		if found[module] != 1 {
			violations = append(violations, fmt.Sprintf("go.mod must require %s exactly once, found %d", module, found[module]))
		}
	}
	sort.Strings(violations)
	return violations
}

func validateAgentWrapperSums(data []byte) []string {
	want := map[string]string{
		agentWrapperModule + " " + agentWrapperVersion:               agentWrapperSum,
		agentWrapperModule + " " + agentWrapperVersion + "/go.mod":   agentWrapperGoModSum,
		runtimeEventsModule + " " + runtimeEventsVersion:             runtimeEventsSum,
		runtimeEventsModule + " " + runtimeEventsVersion + "/go.mod": runtimeEventsGoModSum,
	}
	found := make(map[string]int)
	var violations []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			continue
		}
		key := fields[0] + " " + fields[1]
		expected, ok := want[key]
		if !ok {
			continue
		}
		found[key]++
		if fields[2] != expected {
			violations = append(violations, fmt.Sprintf("%s checksum = %s, want %s", key, fields[2], expected))
		}
	}
	for key := range want {
		if found[key] != 1 {
			violations = append(violations, fmt.Sprintf("go.sum must contain %s exactly once, found %d", key, found[key]))
		}
	}
	sort.Strings(violations)
	return violations
}

func scanRuntimeAgentOwnership(repositoryFS fs.FS) ([]string, error) {
	return scanAgentOwnership(repositoryFS, "internal/runtime/agent", allowedAgentOwnershipSymbols)
}

func scanAgentOwnership(repositoryFS fs.FS, root string, allowed map[agentOwnershipSymbol]agentOwnershipAllowance) ([]string, error) {
	var violations []string
	observed := make(map[agentOwnershipSymbol]int)
	err := fs.WalkDir(repositoryFS, root, func(path string, entry fs.DirEntry, visitErr error) error {
		if visitErr != nil {
			return visitErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := fs.ReadFile(repositoryFS, path)
		if err != nil {
			return err
		}
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, data, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		fileObserved, fileViolations := inspectAgentOwnership(fileSet, path, file, allowed)
		for symbol, count := range fileObserved {
			observed[symbol] += count
		}
		violations = append(violations, fileViolations...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for symbol, allowance := range allowed {
		if observed[symbol] == allowance.count {
			continue
		}
		violations = append(violations, fmt.Sprintf(
			"%s: expected %s.%s exactly %d time(s), found %d (%s); exceptions are exact—update this entry only after ownership review",
			symbol.path, symbol.importPath, symbol.symbol, allowance.count, observed[symbol], allowance.rationale,
		))
	}
	sort.Strings(violations)
	return violations, nil
}

func inspectAgentOwnership(fileSet *token.FileSet, path string, file *ast.File, allowed map[agentOwnershipSymbol]agentOwnershipAllowance) (map[agentOwnershipSymbol]int, []string) {
	var violations []string
	observed := make(map[agentOwnershipSymbol]int)
	aliases := make(map[string]string)
	for _, imported := range file.Imports {
		importPath := strings.Trim(imported.Path.Value, `"`)
		alias := filepath.Base(importPath)
		if imported.Name != nil {
			alias = imported.Name.Name
		}
		if alias == "." {
			if guardedOwnershipImport(importPath) || importPath == "os" || importPath == "os/exec" || importPath == "syscall" {
				violations = append(violations, declarationViolation(fileSet, path, imported.Pos(), "dot import can bypass the Agent Host ownership guard"))
			}
			continue
		}
		if alias == "_" && guardedOwnershipImport(importPath) {
			violations = append(violations, declarationViolation(fileSet, path, imported.Pos(), "blank import has no reviewed Agent Host symbol use; remove it or add an exact path+symbol+count+rationale exception"))
		}
		if alias != "_" {
			aliases[alias] = importPath
		}
		switch importPath {
		case "os/exec":
			violations = append(violations, declarationViolation(fileSet, path, imported.Pos(), "direct os/exec import; go-agent-wrapper owns child process creation"))
		case "syscall":
			violations = append(violations, declarationViolation(fileSet, path, imported.Pos(), "direct syscall process ownership; go-agent-wrapper owns child processes"))
		}
	}

	clientTaint := collectDirectACPClientTaint(fileSet, file, aliases)

	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.TypeSpec:
			if rationale, retired := retiredAgentOwnershipDeclarations[value.Name.Name]; retired {
				violations = append(violations, declarationViolation(fileSet, path, value.Pos(), "retired local lifecycle declaration "+value.Name.Name+"; "+rationale))
			}
		case *ast.FuncDecl:
			if rationale, retired := retiredAgentOwnershipDeclarations[value.Name.Name]; retired {
				violations = append(violations, declarationViolation(fileSet, path, value.Pos(), "retired local lifecycle declaration "+value.Name.Name+"; "+rationale))
			}
		case *ast.Field:
			for _, name := range value.Names {
				if rationale, retired := retiredAgentOwnershipFields[name.Name]; retired {
					violations = append(violations, declarationViolation(fileSet, path, name.Pos(), "retired local lifecycle field "+name.Name+"; "+rationale))
				}
			}
		case *ast.SelectorExpr:
			ident, ok := value.X.(*ast.Ident)
			if ok {
				importPath := aliases[ident.Name]
				if guardedOwnershipImport(importPath) {
					symbol := ownershipSymbol(path, importPath, value.Sel.Name)
					if _, permitted := allowed[symbol]; permitted {
						observed[symbol]++
					} else {
						violations = append(violations, declarationViolation(fileSet, path, value.Pos(), fmt.Sprintf(
							"%s.%s is not an allowed Agent Host dependency; go-agent-wrapper owns process, ACP client, and session lifecycle (add an exact path+symbol+count+rationale exception only for a reviewed DTO/bridge use)",
							filepath.Base(importPath), value.Sel.Name,
						)))
					}
				}
				if importPath == "os/exec" && (value.Sel.Name == "Command" || value.Sel.Name == "CommandContext") {
					violations = append(violations, declarationViolation(fileSet, path, value.Pos(), "direct os/exec "+value.Sel.Name+"; construct processes through go-agent-wrapper"))
				}
				if importPath == "os" && value.Sel.Name == "StartProcess" {
					violations = append(violations, declarationViolation(fileSet, path, value.Pos(), "direct os.StartProcess; construct processes through go-agent-wrapper"))
				}
				if importPath == "syscall" && (value.Sel.Name == "ForkExec" || value.Sel.Name == "Exec" || value.Sel.Name == "StartProcess") {
					violations = append(violations, declarationViolation(fileSet, path, value.Pos(), "direct syscall "+value.Sel.Name+"; construct processes through go-agent-wrapper"))
				}
			}
		case *ast.CallExpr:
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if ok && directACPClientLifecycleMethods[selector.Sel.Name] && clientTaint.contains(selector.X) {
				violations = append(violations, declarationViolation(fileSet, path, selector.Pos(), "direct ACP client lifecycle "+selector.Sel.Name+"; route lifecycle through wrapper.Wrapper/acp.Manager"))
			}
		}
		return true
	})
	return observed, violations
}

func guardedOwnershipImport(importPath string) bool {
	return importPath == agentWrapperModule || strings.HasPrefix(importPath, agentWrapperModule+"/") || importPath == "github.com/hollis-labs/agentkit/agentsessions"
}

var directACPClientLifecycleMethods = map[string]bool{
	"Launch": true, "Prompt": true, "Cancel": true, "Events": true, "InterruptCapability": true, "Close": true,
}

type directACPClientAlias struct {
	target types.Object
	source ast.Expr
}

type directACPClientTaint struct {
	aliases            map[string]string
	objects            map[types.Object]bool
	objectTypes        map[types.Object]types.Object
	directClientFields map[types.Object]map[string]bool
	typeInfo           *types.Info
}

// collectDirectACPClientTaint follows local bindings by go/types object identity,
// rather than identifier spelling, so a client variable in one lexical scope
// cannot taint an unrelated variable with the same name. Alias edges are
// resolved to a fixed point, making declaration and assignment order
// irrelevant to this structural guard.
func collectDirectACPClientTaint(fileSet *token.FileSet, file *ast.File, aliases map[string]string) *directACPClientTaint {
	typeInfo := &types.Info{
		Defs: make(map[*ast.Ident]types.Object),
		Uses: make(map[*ast.Ident]types.Object),
	}
	typeConfig := &types.Config{
		Importer: importer.Default(),
		Error:    func(error) {},
	}
	_, _ = typeConfig.Check(file.Name.Name, fileSet, []*ast.File{file}, typeInfo)

	taint := &directACPClientTaint{
		aliases:            aliases,
		objects:            make(map[types.Object]bool),
		objectTypes:        make(map[types.Object]types.Object),
		directClientFields: make(map[types.Object]map[string]bool),
		typeInfo:           typeInfo,
	}
	var edges []directACPClientAlias

	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.TypeSpec:
			structure, ok := value.Type.(*ast.StructType)
			typeObject := typeInfo.Defs[value.Name]
			if !ok || typeObject == nil {
				break
			}
			for _, field := range structure.Fields.List {
				if !isDirectACPClientType(field.Type, aliases) {
					continue
				}
				if taint.directClientFields[typeObject] == nil {
					taint.directClientFields[typeObject] = make(map[string]bool)
				}
				for _, name := range field.Names {
					taint.directClientFields[typeObject][name.Name] = true
				}
			}
		case *ast.ValueSpec:
			for _, name := range value.Names {
				if isDirectACPClientType(value.Type, aliases) {
					taint.mark(name)
				}
				if target, typeObject := typeInfo.Defs[name], taint.localNamedTypeObject(value.Type); target != nil && typeObject != nil {
					taint.objectTypes[target] = typeObject
				}
			}
			for index, expression := range value.Values {
				if index >= len(value.Names) {
					continue
				}
				target := typeInfo.Defs[value.Names[index]]
				edges = append(edges, directACPClientAlias{target: target, source: expression})
				if typeObject := taint.localCompositeTypeObject(expression); typeObject != nil && target != nil {
					taint.objectTypes[target] = typeObject
				}
			}
		case *ast.Field:
			for _, name := range value.Names {
				if isDirectACPClientType(value.Type, aliases) {
					taint.mark(name)
				}
				if target, typeObject := typeInfo.Defs[name], taint.localNamedTypeObject(value.Type); target != nil && typeObject != nil {
					taint.objectTypes[target] = typeObject
				}
			}
		case *ast.AssignStmt:
			for index, expression := range value.Rhs {
				if index >= len(value.Lhs) {
					continue
				}
				name, ok := value.Lhs[index].(*ast.Ident)
				if !ok {
					continue
				}
				target := typeInfo.ObjectOf(name)
				edges = append(edges, directACPClientAlias{target: target, source: expression})
				if typeObject := taint.localCompositeTypeObject(expression); typeObject != nil && target != nil {
					taint.objectTypes[target] = typeObject
				}
			}
		}
		return true
	})

	for changed := true; changed; {
		changed = false
		for _, edge := range edges {
			if edge.target == nil || taint.objects[edge.target] || !taint.contains(edge.source) {
				continue
			}
			taint.objects[edge.target] = true
			changed = true
		}
	}
	return taint
}

func (t *directACPClientTaint) mark(name *ast.Ident) {
	if name != nil {
		if object := t.typeInfo.ObjectOf(name); object != nil {
			t.objects[object] = true
		}
	}
}

func (t *directACPClientTaint) contains(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return t.contains(value.X)
	case *ast.StarExpr:
		return t.contains(value.X)
	case *ast.UnaryExpr:
		return t.contains(value.X)
	case *ast.Ident:
		return t.objects[t.typeInfo.ObjectOf(value)]
	case *ast.SelectorExpr:
		receiver, ok := value.X.(*ast.Ident)
		if !ok {
			return false
		}
		typeObject := t.objectTypes[t.typeInfo.ObjectOf(receiver)]
		return typeObject != nil && t.directClientFields[typeObject][value.Sel.Name]
	case *ast.CallExpr:
		return isDirectACPClientConstructor(value, t.aliases)
	default:
		return false
	}
}

func (t *directACPClientTaint) localNamedTypeObject(expression ast.Expr) types.Object {
	switch value := expression.(type) {
	case *ast.Ident:
		return t.typeInfo.ObjectOf(value)
	case *ast.ParenExpr:
		return t.localNamedTypeObject(value.X)
	case *ast.StarExpr:
		return t.localNamedTypeObject(value.X)
	default:
		return nil
	}
}

func (t *directACPClientTaint) localCompositeTypeObject(expression ast.Expr) types.Object {
	switch value := expression.(type) {
	case *ast.CompositeLit:
		return t.localNamedTypeObject(value.Type)
	case *ast.UnaryExpr:
		return t.localCompositeTypeObject(value.X)
	case *ast.ParenExpr:
		return t.localCompositeTypeObject(value.X)
	default:
		return nil
	}
}

var retiredAgentOwnershipDeclarations = map[string]string{
	"acpSession":                "wrapper's ACP Session/Manager owns transport and event draining",
	"nativeAdapter":             "adapters.Select owns native adapter construction",
	"envWrappedCLIAdapter":      "wrapper.ChildEnvironment owns child environment materialization",
	"newACPClient":              "shipped adapter factories create clients behind wrapper ownership",
	"bootACP":                   "wrapper.Run owns ACP launch and readiness",
	"newNativeAdapter":          "adapters.Select owns native adapter construction",
	"runtimeConfigForAdapter":   "wrapper adapter selection owns runtime configuration",
	"protocolTransportFromCaps": "wrapper adapter descriptors own protocol/transport facts",
	"wrapEnvForSpawn":           "wrapper.ChildEnvironment owns child environment materialization",
	"writeEnvWrapperScript":     "generated shell launchers were removed in favor of direct child environment DTOs",
	"shellQuote":                "generated shell launchers were removed",
	"shouldUsePTY":              "adapters.Select owns native launch-mode selection",
	"trackLiveSession":          "SessionManager is the single runtime-ID binding registry",
	"untrackLiveSession":        "SessionManager is the single runtime-ID binding registry",
	"StopAllLiveSessions":       "SessionManager.Shutdown delegates exact wrappers concurrently",
}

var retiredAgentOwnershipFields = map[string]string{
	"liveSessions":    "the duplicate sync.Map registry was replaced by SessionManager",
	"SessionsManager": "the direct agentkit manager dependency was removed",
}

func isDirectACPClientType(expression ast.Expr, aliases map[string]string) bool {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return isDirectACPClientType(value.X, aliases)
	case *ast.StarExpr:
		return isDirectACPClientType(value.X, aliases)
	case *ast.SelectorExpr:
		ident, ok := value.X.(*ast.Ident)
		if !ok {
			return false
		}
		importPath := aliases[ident.Name]
		return value.Sel.Name == "Client" && (importPath == agentWrapperModule+"/acp" || strings.HasPrefix(importPath, agentWrapperModule+"/adapters/"))
	default:
		return false
	}
}

func isDirectACPClientConstructor(expression ast.Expr, aliases map[string]string) bool {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	importPath := aliases[ident.Name]
	return (importPath == agentWrapperModule+"/acp" && strings.HasPrefix(selector.Sel.Name, "New")) ||
		(strings.HasPrefix(importPath, agentWrapperModule+"/adapters/") && selector.Sel.Name == "NewClient")
}
