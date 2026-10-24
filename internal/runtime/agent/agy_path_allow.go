package agent

import (
	"os"
	"path/filepath"
	"strings"
)

// agy_path_allow.go: the allow rules that let a Nanite-launched Antigravity
// (agy) agent read and write files under the directories Nanite already offers
// every CLI agent, and nowhere else (CW-20261024-0110).
//
// Headless agy auto-denies the non-MCP tools (run_command, file read and
// write, read_url) unless settings.json `permissions.allow` carries a rule for
// them. agy's grammar (its own text): `read_file(<absolute_path>)`,
// `write_file(<absolute_path>)`, `command(<prefix>)`, `read_url(<domain>)`,
// never `*`, never `/`.
//
// Source: SetupParams.CLIWritableRoots, the same list codex and claude get
// (effectiveCLIWritableRoots): the session work root, the host settings
// `directories`, and the configured dev_tools_allowed_paths, each an existing
// directory and none inside a control-plane directory. A root yields
// `read_file(<root>)` and `write_file(<root>)`, the work root first.
//
// Not mapped, because Nanite has no per-agent policy field carrying scope for
// them: `command(...)` (grants are tool names, not command prefixes) and
// `read_url(...)` / `execute_url(...)` (no domain list). They stay denied.

// agyPathRuleBreakers cannot appear in a path that gets a rule: `)` ends the
// rule early, a newline or NUL breaks the line, `*` would read as a wildcard,
// and whitespace has no documented quoting (fail closed: no rule).
const agyPathRuleBreakers = "()*\n\r\x00 \t"

// agySecretHomeDirs are real-home subdirectories no rule may cover or sit in.
var agySecretHomeDirs = []string{".ssh", ".gnupg", ".aws", ".kube", ".docker", ".gemini", ".agents", ".nanite"}

// agyPathAllowRules is the read and write rule set for roots. Roots that are
// unsafe (see agySafeRoot) are skipped. Deterministic order: every read rule in
// root order, then every write rule.
func agyPathAllowRules(roots []string, realHome string) []string {
	var safe []string
	seen := map[string]bool{}
	for _, r := range roots {
		if !agySafeRoot(r, realHome) || seen[r] {
			continue
		}
		seen[r] = true
		safe = append(safe, r)
	}
	var rules []string
	for _, r := range safe {
		rules = append(rules, "read_file("+r+")")
	}
	for _, r := range safe {
		rules = append(rules, "write_file("+r+")")
	}
	return rules
}

// agySafeRoot reports whether root may become a rule: absolute and already
// clean, free of rule-breaking characters, not `/`, not the real home or a
// parent of it, and not a secrets directory of the home or inside one. Symlinks
// are resolved for the home checks, so a link to the home does not slip by.
func agySafeRoot(root, realHome string) bool {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.ContainsAny(root, agyPathRuleBreakers) {
		return false
	}
	if realHome == "" || !filepath.IsAbs(realHome) {
		return false
	}
	homes := []string{filepath.Clean(realHome)}
	if h, err := filepath.EvalSymlinks(realHome); err == nil {
		homes = append(homes, h)
	}
	cands := []string{root}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		cands = append(cands, r)
	}
	for _, c := range cands {
		if c == string(filepath.Separator) {
			return false
		}
		for _, h := range homes {
			if c == h || strings.HasPrefix(h, c+string(filepath.Separator)) {
				return false
			}
			for _, s := range agySecretHomeDirs {
				sd := filepath.Join(h, s)
				if c == sd || strings.HasPrefix(c, sd+string(filepath.Separator)) {
					return false
				}
			}
		}
	}
	return true
}

// agyAllowRules is every rule the launch's private settings carry.
func agyAllowRules(params SetupParams) []string {
	rules := agyMCPAllowRules(params)
	realHome, _ := os.UserHomeDir()
	return append(rules, agyPathAllowRules(params.CLIWritableRoots, realHome)...)
}
