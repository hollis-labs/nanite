package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hollis-labs/libs/agent/agentdir"
	hotel "github.com/hollis-labs/libs/core/util/otel"
	libpolicy "github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/authored"
	"github.com/hollis-labs/nanite/internal/definition"
	"github.com/hollis-labs/nanite/internal/headless"
	"github.com/hollis-labs/nanite/internal/provider"
	"github.com/hollis-labs/nanite/internal/repl"
	"github.com/hollis-labs/nanite/internal/subagent"
	"github.com/hollis-labs/substrate/module"
	"github.com/hollis-labs/substrate/modules/cli"
	"github.com/hollis-labs/substrate/modules/database"
	"github.com/hollis-labs/substrate/modules/logging"
	"github.com/hollis-labs/substrate/modules/policy"
	"github.com/hollis-labs/substrate/modules/secrets"
	"github.com/spf13/cobra"
)

func extractDir(args []string) (string, []string, error) {
	dir := "."
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			out = append(out, args[i:]...)
			break
		}
		switch {
		case arg == "--dir" || arg == "-C":
			rest := args[i+1:]
			if len(rest) == 0 || rest[0] == "" {
				return "", nil, fmt.Errorf("%s requires a directory", arg)
			}
			dir = rest[0]
			i++
		case strings.HasPrefix(arg, "--dir="):
			dir = strings.TrimPrefix(arg, "--dir=")
		case strings.HasPrefix(arg, "-C="):
			dir = strings.TrimPrefix(arg, "-C=")
		case arg == "--prompt" || arg == "-p":
			// -p may stand alone, as claude's does (the prompt is then an argument or stdin).
			out = append(out, arg)
			if rest := args[i+1:]; len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
				out = append(out, rest[0])
				i++
			}
		case arg == "--resume" || arg == "--output" || arg == "--events" || arg == "--session-id" ||
			arg == "--input-format" || arg == "--output-format":
			out = append(out, arg)
			if rest := args[i+1:]; len(rest) > 0 {
				out = append(out, rest[0])
				i++
			}
		default:
			out = append(out, arg)
		}
	}
	abs, err := filepath.Abs(dir)
	return abs, out, err
}

type appPDP struct{ authorizer *policy.Authorizer }

func (p appPDP) Evaluate(ctx context.Context, req libpolicy.Request) (libpolicy.Decision, error) {
	result, err := p.authorizer.Evaluate(ctx, req)
	return result.Decision, err
}

// gatewaySettings reads nanite.gateway (url or base_url, key_ref, auth, headers) from the
// configuration, with NANITE_GATEWAY_URL and NANITE_GATEWAY_KEY_REF over it.
func gatewaySettings(app *module.App) (agent.Gateway, error) {
	top, _ := app.Config["nanite"].(map[string]any)
	settings, _ := top["gateway"].(map[string]any)
	base, _ := settings["url"].(string)
	if base == "" {
		base, _ = settings["base_url"].(string)
	}
	ref, _ := settings["key_ref"].(string)
	auth, _ := settings["auth"].(string)
	headers := make(map[string]string)
	if values, ok := settings["headers"].(map[string]any); ok {
		for k, v := range values {
			value, ok := v.(string)
			if !ok {
				return agent.Gateway{}, fmt.Errorf("gateway header %q must be a string", k)
			}
			headers[k] = value
		}
	}
	if value := os.Getenv("NANITE_GATEWAY_URL"); value != "" {
		base = value
	}
	if value := os.Getenv("NANITE_GATEWAY_KEY_REF"); value != "" {
		ref = value
	}
	return agent.Gateway{BaseURL: base, Headers: headers, KeyRef: ref, Auth: auth}, nil
}

func openAgent(cmd *cobra.Command, app *module.App, dir string, gate agent.Gate, sink agent.EventSink, adjust ...func(*agent.Options)) (*agent.Agent, error) {
	gw, err := gatewaySettings(app)
	if err != nil {
		return nil, err
	}
	value, _ := app.Value(logging.Key)
	logger, _ := value.(*slog.Logger)
	// The terminal agent gets every built-in group, whatever kind its directory declares.
	opts := agent.Options{Dir: dir, Store: database.From(app), Gate: gate, Logger: logger, Events: sink, Builtins: agent.AllBuiltins(), TraceFiles: true,
		Secrets: secrets.Resolver(app), PDP: appPDP{policy.From(app)}, Gateway: gw,
	}
	for _, f := range adjust {
		f(&opts)
	}
	return agent.Open(cmd.Context(), opts)
}

func newRoot(app *module.App, dir string) *cobra.Command {
	root := cli.Root(app)
	stopCmd := &cobra.Command{Use: "stop --all", Short: "Cancel all work in the running agent; reopen it to accept work again", Args: cobra.NoArgs}
	stopCmd.Flags().Bool("all", false, "Stop all turns, subtasks and background jobs")
	root.AddCommand(stopCmd, toolsCommand(dir), agentsCommand(), authCommand(app, dir))
	root.Version = naniteVersion()
	root.SetVersionTemplate("{{.Version}} (Nanite)\n")
	root.SilenceUsage = true
	root.SilenceErrors = true
	var prompt, output, resume, discard, eventPath, inputFormat, outputFormat, sessionID string
	var latest, verbose bool
	root.Flags().StringVarP(&prompt, "prompt", "p", "", "Run without the terminal: one prompt (-p \"...\" or -p \"...\" as an argument), or turns from stdin with --input-format stream-json")
	root.Flags().Lookup("prompt").NoOptDefVal = " "
	root.Flags().StringVar(&inputFormat, "input-format", "", "With -p: text (the prompt) or stream-json (user messages and control requests on stdin, as claude -p reads them)")
	root.Flags().StringVar(&outputFormat, "output-format", "", "With -p: text, json or stream-json in Claude Code's message shapes")
	root.Flags().StringVar(&sessionID, "session-id", "", "Use this conversation id, creating the conversation if it is new")
	var claude claudeFlags
	claude.register(root)
	root.Flags().BoolVar(&verbose, "verbose", false, "Accepted for compatibility with claude -p (stream-json output is always complete)")
	root.Flags().StringVar(&output, "output", "text", "Output format for -p: text, json or stream-json")
	root.Flags().BoolVar(&latest, "continue", false, "Continue the latest conversation")
	root.Flags().StringVar(&resume, "resume", "", "Resume an interrupted turn in the given conversation")
	root.Flags().StringVar(&discard, "discard", "", "Abandon the interrupted turn in the given conversation")
	root.Flags().StringVar(&eventPath, "events", "", "Runtime event log path (default .nanite/events.jsonl, - for stdout)")
	root.PersistentFlags().StringP("dir", "C", dir, "Agent directory")
	root.Args = cobra.MaximumNArgs(1)
	root.RunE = func(cmd *cobra.Command, args []string) (retErr error) {
		nonInteractive := cmd.Flags().Changed("prompt") || inputFormat == headless.StreamJSON
		prompt = strings.TrimSpace(prompt)
		if len(args) == 1 {
			if !nonInteractive || prompt != "" {
				return fmt.Errorf("unexpected argument %q (the prompt goes after -p)", args[0])
			}
			prompt = args[0]
		}
		claudeStyle := inputFormat != "" || outputFormat != ""
		if sessionID != "" && (resume != "" || latest) {
			return errors.New("--session-id cannot be combined with --resume or --continue")
		}
		var gate agent.Gate
		var terminal repl.Terminal
		if !nonInteractive {
			terminal = repl.NewTerminal(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
			gate = terminal
		}
		sink, closeEvents, err := cliEvents(dir, eventPath, cmd.OutOrStdout())
		if err != nil {
			return err
		}
		defer func() { retErr = errors.Join(retErr, closeEvents()) }()
		if os.Getenv("NANITE_OTEL_DISABLED") != "1" &&
			(hotel.EnabledFromEnv("NANITE_OTEL_ENABLED") || os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "") {
			defer hotel.InitOrWarn(cmd.Context(), func(format string, args ...any) { _, _ = fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...) },
				5*time.Second, hotel.WithServiceName("nanite"))()
		}
		var opts agent.Options
		promptGate, closeGate, err := claude.apply(app, dir, cmd.ErrOrStderr(), &opts)
		if err != nil {
			return err
		}
		defer func() { retErr = errors.Join(retErr, closeGate()) }()
		if promptGate != nil {
			if !nonInteractive {
				return errPromptToolInteractive
			}
			gate = promptGate
		}
		a, err := openAgent(cmd, app, dir, gate, sink, func(o *agent.Options) {
			o.Model, o.PermissionMode, o.MCPServers, o.StrictMCP = opts.Model, opts.PermissionMode, opts.MCPServers, opts.StrictMCP
			o.Instructions, o.PermissionRules, o.AddDirs, o.AddSkillDirs = opts.Instructions, opts.PermissionRules, opts.AddDirs, opts.AddSkillDirs
		})
		if err != nil {
			return err
		}
		defer func() { retErr = errors.Join(retErr, a.Close()) }()
		if discard != "" {
			target, err := a.Conversation(cmd.Context(), discard)
			if err != nil {
				return err
			}
			if err := target.Discard(cmd.Context()); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.ErrOrStderr(), "Discarded the interrupted turn in conversation %s.\n", discard)
			if err != nil || !nonInteractive {
				return err
			}
		}
		var conversation *agent.Conversation
		if sessionID != "" {
			conversation, err = a.EnsureConversation(cmd.Context(), sessionID)
		} else {
			conversation, err = selectConversation(cmd.Context(), a, resume, latest)
		}
		if err != nil {
			return err
		}
		if interrupted := conversation.Interrupted(); interrupted != nil {
			if nonInteractive {
				switch {
				case resume != "":
					run, err := conversation.Resume(cmd.Context())
					if err != nil {
						return err
					}
					if err := repl.Output(run, "text", cmd.ErrOrStderr()); err != nil {
						return err
					}
				case latest:
					return fmt.Errorf("conversation %s has interrupted turn %s; use --resume %s or --discard %s", conversation.ID(), interrupted.ID, conversation.ID(), conversation.ID())
				default:
					// A one-shot prompt is a new conversation; an unfinished turn elsewhere must not block it.
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "note: conversation %s has an interrupted turn; continue it with --resume %s or drop it with --discard %s\n", conversation.ID(), conversation.ID(), conversation.ID())
					conversation, err = a.NewConversation(cmd.Context())
					if err != nil {
						return err
					}
				}
			} else {
				choice := repl.ResumeTurn
				if resume == "" {
					choice, err = terminal.ConfirmResume(cmd.Context(), conversation.ID())
					if err != nil {
						return err
					}
				}
				switch choice {
				case repl.ResumeTurn:
					if err := terminal.Resume(cmd.Context(), conversation.Resume); err != nil {
						return err
					}
				case repl.DiscardTurn:
					// The conversation stays selected: what it already holds is kept.
					if err := conversation.Discard(cmd.Context()); err != nil {
						return err
					}
				default:
					conversation, err = a.NewConversation(cmd.Context())
					if err != nil {
						return err
					}
				}
			}
		}

		if nonInteractive && claudeStyle {
			return runHeadless(cmd, a, conversation, headless.Options{
				SessionID: conversation.ID(), Prompt: prompt, InputFormat: inputFormat, OutputFormat: outputFormat,
				Model: opts.Model, PermissionMode: firstNonEmpty(claude.permissionMode, opts.PermissionMode), MCPServers: sortedKeys(opts.MCPServers),
			})
		}
		if nonInteractive {
			if prompt == "" {
				return errors.New("no prompt: give -p \"...\" or use --input-format stream-json")
			}
			return repl.One(cmd.Context(), conversation, prompt, output, cmd.OutOrStdout())
		}
		return terminal.Chat(cmd.Context(), func(ctx context.Context) (repl.Sender, error) {
			return a.NewConversation(ctx)
		}, conversation)
	}
	root.AddCommand(
		&cobra.Command{Use: "init [dir]", Short: "Create a starter agent directory", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return initAgent(args[0])
			}
			return initAgent(dir)
		}},
		&cobra.Command{Use: "validate [dir]", Short: "Validate authored agent files", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			path := dir
			if len(args) == 1 {
				path = args[0]
			}
			info, err := agentdir.Load(path)
			if err != nil {
				return err
			}
			if err := subagent.CheckLimit(info.Profile.Runtime.SubtaskLimit); err != nil {
				return fmt.Errorf("host.yaml runtime: %w", err)
			}
			variants, err := subagent.Load(path)
			if err != nil {
				return err
			}
			toolPkgs, err := authored.LoadTools(path)
			if err != nil {
				return err
			}
			def, err := definition.Load(path, "-")
			if err != nil {
				return err
			}
			for _, name := range slices.Sorted(maps.Keys(def.Providers)) {
				if def.Providers[name].Auth == provider.AuthWarden && (def.Identity == nil || def.Identity.Warden == nil) {
					return fmt.Errorf("providers.yaml: provider %s uses auth warden, but identity.yaml has no warden section", name)
				}
			}
			for _, name := range subagent.Names(variants) {
				p := variants[name].Provider
				if _, named := def.Providers[p]; p != "" && !named && p != provider.BuiltinAnthropic && p != provider.BuiltinOpenAI {
					// An embedding host can define providers this directory does not.
					if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: variants/%s.md: provider %q is not defined here; it must come from the embedding host\n", name, p); err != nil {
						return err
					}
				}
			}
			for _, name := range authored.ToolNames(toolPkgs) {
				if len(toolPkgs[name].Needs) > 0 && (def.Identity == nil || def.Identity.Warden == nil) {
					return fmt.Errorf("tools/%s: it needs Warden tokens, but identity.yaml has no warden section", name)
				}
			}
			var own []string
			for _, name := range subagent.Names(variants) {
				if !variants[name].Builtin {
					own = append(own, name)
				}
			}
			if _, err = fmt.Fprintln(cmd.OutOrStdout(), info.Definition.Name); err != nil {
				return err
			}
			if len(own) > 0 {
				if _, err = fmt.Fprintln(cmd.OutOrStdout(), "variants: "+strings.Join(own, ", ")); err != nil {
					return err
				}
			}
			if names := authored.ToolNames(toolPkgs); len(names) > 0 {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "tools: "+strings.Join(names, ", "))
			}
			return err
		}},
		&cobra.Command{Use: "conversations", Short: "List conversations", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) (retErr error) {
			a, err := openAgent(cmd, app, dir, nil, nil)
			if err != nil {
				return err
			}
			defer func() { retErr = errors.Join(retErr, a.Close()) }()
			conversations, err := a.Conversations(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(conversations)
			}
			return printConversations(cmd.OutOrStdout(), conversations)
		}},
		&cobra.Command{Use: "rebuild-index", Short: "Rebuild the conversation index from JSONL transcripts", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) (retErr error) {
				a, err := openAgent(cmd, app, dir, nil, nil)
				if err != nil {
					return err
				}
				defer func() { retErr = errors.Join(retErr, a.Close()) }()
				return a.RebuildIndex(cmd.Context())
			},
		},
	)
	if list, _, err := root.Find([]string{"conversations"}); err == nil {
		list.Flags().Bool("json", false, "Print the full records as JSON")
	}
	return root
}

func selectConversation(ctx context.Context, a *agent.Agent, resume string, latest bool) (*agent.Conversation, error) {
	if resume != "" {
		return a.Conversation(ctx, resume)
	}
	if latest {
		c, err := a.Latest(ctx)
		if !errors.Is(err, agent.ErrNoConversation) {
			return c, err
		}
	} else {
		conversations, err := a.Conversations(ctx)
		if err != nil {
			return nil, err
		}
		for _, info := range conversations {
			c, err := a.Conversation(ctx, info.ID)
			if err != nil {
				return nil, err
			}
			if c.Interrupted() != nil {
				return c, nil
			}
		}
	}

	return a.NewConversation(ctx)
}

// printConversations lists top-level conversations newest first, with the
// subagent conversations each one started indented beneath it.
func printConversations(w io.Writer, all []agent.ConversationInfo) error {
	if len(all) == 0 {
		_, err := fmt.Fprintln(w, "No conversations yet.")
		return err
	}
	children := map[string][]agent.ConversationInfo{}
	for _, c := range all {
		if c.ParentID != "" {
			children[c.ParentID] = append(children[c.ParentID], c)
		}
	}
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "ID\tUPDATED\tROLE\tTITLE")
	row := func(c agent.ConversationInfo, indent string) {
		title := c.Title
		if title == "" {
			title = "(untitled)"
		}
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\t%s%s\n", c.ID, c.UpdatedAt.Local().Format("2006-01-02 15:04"), c.Role, indent, title)
	}
	for _, c := range all {
		if c.ParentID != "" {
			continue
		}
		row(c, "")
		for _, child := range children[c.ID] {
			row(child, "  ")
		}
	}
	return table.Flush()
}
