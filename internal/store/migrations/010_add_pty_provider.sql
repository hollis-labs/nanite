-- Add PTY (Claude CLI) provider and model for existing databases.
INSERT OR IGNORE INTO providers (id, name, provider_type, api_key)
VALUES ('pty-001', 'Claude CLI (PTY)', 'pty', '');

INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
VALUES ('claude-cli', 'pty-001', 'claude-cli', 'Claude CLI', 0, 0, 1);
