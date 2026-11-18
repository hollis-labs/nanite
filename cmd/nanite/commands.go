package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hollis-labs/libs/agent/agentdir"
	hotel "github.com/hollis-labs/libs/core/util/otel"
	libpolicy "github.com/hollis-labs/libs/security/policy"
	"github.com/hollis-labs/nanite/agent"
	"github.com/hollis-labs/nanite/internal/repl"
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
			i++
			if i == len(args) || args[i] == "" {
				return "", nil, fmt.Errorf("%s requires a directory", arg)
			}
			dir = args[i]
		case strings.HasPrefix(arg, "--dir="):
			dir = strings.TrimPrefix(arg, "--dir=")
		case strings.HasPrefix(arg, "-C="):
			dir = strings.TrimPrefix(arg, "-C=")
		case arg == "--prompt" || arg == "-p" || arg == "--resume" || arg == "--output" || arg == "--events":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
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

func openAgent(cmd *cobra.Command, app *module.App, dir string, gate agent.Gate, sink agent.EventSink) (*agent.Agent, error) {
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
				return nil, fmt.Errorf("gateway header %q must be a string", k)
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
	value, _ := app.Value(logging.Key)
	logger, _ := value.(*slog.Logger)
	return agent.Open(cmd.Context(), agent.Options{Dir: dir, Store: database.From(app), Gate: gate, Logger: logger, Events: sink,
		Secrets: secrets.Resolver(app), PDP: appPDP{policy.From(app)}, Gateway: agent.Gateway{BaseURL: base, Headers: headers, KeyRef: ref, Auth: auth},
	})
}

func newRoot(app *module.App, dir string) *cobra.Command {
	root := cli.Root(app)
	root.SilenceUsage = true
	root.SilenceErrors = true
	var prompt, output, resume, discard, eventPath string
	var latest bool
	root.Flags().StringVarP(&prompt, "prompt", "p", "", "Run one prompt")
	root.Flags().StringVar(&output, "output", "text", "Output format for -p: text, json or stream-json")
	root.Flags().BoolVar(&latest, "continue", false, "Continue the latest conversation")
	root.Flags().StringVar(&resume, "resume", "", "Resume an interrupted turn in the given conversation")
	root.Flags().StringVar(&discard, "discard", "", "Abandon the interrupted turn in the given conversation")
	root.Flags().StringVar(&eventPath, "events", "", "Runtime event log path (default .nanite/events.jsonl, - for stdout)")
	root.PersistentFlags().StringP("dir", "C", dir, "Agent directory")
	root.Args = cobra.NoArgs
	root.RunE = func(cmd *cobra.Command, _ []string) (retErr error) {
		nonInteractive := cmd.Flags().Changed("prompt")
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
		a, err := openAgent(cmd, app, dir, gate, sink)
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
		conversation, err := selectConversation(cmd.Context(), a, resume, latest)
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

		if nonInteractive {
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
			_, err = fmt.Fprintln(cmd.OutOrStdout(), info.Definition.Name)
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
