-- Add Codex CLI and Gemini CLI providers and models for existing databases.

-- Codex CLI
INSERT OR IGNORE INTO providers (id, name, provider_type, api_key)
VALUES ('pty-codex-001', 'Codex CLI (PTY)', 'pty-codex', '');

INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
VALUES ('codex-cli', 'pty-codex-001', 'codex-cli', 'Codex CLI', 0, 0, TRUE);

-- Gemini CLI
INSERT OR IGNORE INTO providers (id, name, provider_type, api_key)
VALUES ('pty-gemini-001', 'Gemini CLI (PTY)', 'pty-gemini', '');

INSERT OR IGNORE INTO models (id, provider_id, model_id, display_name, context_window, max_output, supports_tools)
VALUES ('gemini-cli', 'pty-gemini-001', 'gemini-cli', 'Gemini CLI', 0, 0, TRUE);
