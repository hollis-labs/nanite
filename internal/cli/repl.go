package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// Config holds CLI session configuration.
type Config struct {
	ServerURL   string
	Agent       string
	Mode        string
	WorkspaceID string
	ProjectID   string
	NoColor     bool
	ShowUsage   bool
	AutoStart   bool
}

// REPL implements the read-eval-print loop for the CLI.
type REPL struct {
	client   *Client
	renderer *Renderer
	config   Config
	session  *Session
}

// NewREPL creates a new REPL instance.
func NewREPL(cfg Config) *REPL {
	return &REPL{
		client: NewClient(cfg.ServerURL),
		renderer: &Renderer{
			NoColor:   cfg.NoColor,
			ShowUsage: cfg.ShowUsage,
		},
		config: cfg,
	}
}

// Run starts the REPL loop. It blocks until the user quits or the context is cancelled.
func (r *REPL) Run(ctx context.Context) error {
	// Ensure server is reachable (with auto-start if configured).
	if err := r.ensureServer(); err != nil {
		return fmt.Errorf("cannot connect to server: %w", err)
	}

	health, err := r.client.Health()
	if err != nil {
		return fmt.Errorf("server health check failed: %w", err)
	}

	r.renderer.PrintBanner(health.Version, r.config.ServerURL)

	// Resolve workspace ID if not provided.
	if r.config.WorkspaceID == "" {
		ws, err := r.client.ListWorkspaces()
		if err != nil {
			return fmt.Errorf("failed to list workspaces: %w", err)
		}
		if len(ws) == 0 {
			return fmt.Errorf("no workspaces found — create one in the web UI first")
		}
		r.config.WorkspaceID = ws[0].ID
		r.renderer.PrintInfo("Using workspace: %s", ws[0].Name)
	}

	// Create a session.
	sess, err := r.client.CreateSession(CreateSessionRequest{
		WorkspaceID: r.config.WorkspaceID,
		ProjectID:   r.config.ProjectID,
	})
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	r.session = sess
	r.renderer.PrintInfo("Session: %s", sess.ShortCode)

	// Handle Ctrl+C gracefully.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	go func() {
		for range sigCh {
			fmt.Fprintln(os.Stderr, "\n(interrupt — type /quit to exit)")
			r.renderer.PrintPrompt("mentat")
		}
	}()

	// Main loop.
	scanner := bufio.NewScanner(os.Stdin)
	agentName := "mentat"
	if r.config.Agent != "" {
		agentName = r.config.Agent
	}

	for {
		r.renderer.PrintPrompt(agentName)
		if !scanner.Scan() {
			break // EOF
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// Handle CLI commands.
		if strings.HasPrefix(input, "/") {
			if r.handleCommand(input) {
				continue
			}
			return nil // /quit
		}

		// Send message and stream response.
		if err := r.sendAndStream(input); err != nil {
			r.renderer.PrintError("Error: %v", err)
		}
	}

	return nil
}

// sendAndStream sends a message and renders the streaming response.
func (r *REPL) sendAndStream(content string) error {
	resp, err := r.client.SendMessage(r.session.ID, content)
	if err != nil {
		return err
	}

	return r.client.StreamResponse(resp.StreamURL, r.renderer.RenderEvent)
}

// handleCommand processes /commands. Returns true to continue the loop, false to quit.
func (r *REPL) handleCommand(input string) bool {
	parts := strings.Fields(input)
	cmd := parts[0]

	switch cmd {
	case "/quit", "/exit", "/q":
		r.renderer.PrintInfo("Goodbye.")
		return false

	case "/session":
		if r.session != nil {
			fmt.Fprintf(os.Stderr, "Session: %s (%s)\n", r.session.ShortCode, r.session.ID)
		}
		return true

	case "/mode":
		if len(parts) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: /mode <mode-name>")
		} else {
			r.renderer.PrintInfo("Mode switching via CLI: coming soon")
		}
		return true

	case "/help":
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  /quit, /exit, /q  — Exit the CLI")
		fmt.Fprintln(os.Stderr, "  /session          — Show current session info")
		fmt.Fprintln(os.Stderr, "  /mode <name>      — Switch agent mode")
		fmt.Fprintln(os.Stderr, "  /help             — Show this help")
		return true

	default:
		// Unknown command — send as a message (slash commands pass through to the agent).
		if err := r.sendAndStream(input); err != nil {
			r.renderer.PrintError("Error: %v", err)
		}
		return true
	}
}

// ensureServer checks if the server is reachable, optionally auto-starting it.
func (r *REPL) ensureServer() error {
	// Try connecting.
	if _, err := r.client.Health(); err == nil {
		return nil
	}

	if !r.config.AutoStart {
		return fmt.Errorf("server not reachable at %s (use --auto-start to launch automatically)", r.config.ServerURL)
	}

	// Auto-start the server in the background.
	r.renderer.PrintInfo("Starting mentat-chat serve in background...")

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}

	cmd := exec.Command(exe, "serve")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	// Wait for server to become healthy (max 5 seconds).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
		if _, err := r.client.Health(); err == nil {
			r.renderer.PrintInfo("Server started (PID %d)", cmd.Process.Pid)
			return nil
		}
	}

	return fmt.Errorf("server started but did not become healthy within 5 seconds")
}
