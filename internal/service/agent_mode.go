package service

import (
	"os"
	"strings"
)

// Environment inputs to the default agent mode. A Nanite run by Cerberus can be
// given the labels of its own resource (the manifest's env and admin) through
// these; nothing else is read, and a host name is not an environment signal.
const (
	// EnvResourceEnv carries the service's Cerberus `env` label (for example "dev").
	EnvResourceEnv = "NANITE_RESOURCE_ENV"
	// EnvResourceAdmin carries the service's Cerberus `admin` label (for example "owner").
	EnvResourceAdmin = "NANITE_RESOURCE_ADMIN"
	// EnvAgentMode is the application setting for the default mode: live or pinned.
	EnvAgentMode = "NANITE_AGENT_MODE"
)

// DeployLabels are the labels of the target service Nanite runs as.
type DeployLabels struct {
	Env   string
	Admin string
}

// DeployLabelsFromEnv reads the labels from the environment.
func DeployLabelsFromEnv() DeployLabels {
	return DeployLabels{Env: strings.TrimSpace(os.Getenv(EnvResourceEnv)), Admin: strings.TrimSpace(os.Getenv(EnvResourceAdmin))}
}

// AppDefaultModeFromEnv reads the application's default mode, or "".
func AppDefaultModeFromEnv() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv(EnvAgentMode)))
}

// ResolveAgentMode decides whether edits to an agent directory reach new
// sessions on their own ("live") or only by an explicit operator action
// ("pinned"). One rule, first match wins:
//
//  1. the host's own mode (host.yaml);
//  2. the application setting for the default mode;
//  3. pinned, unless the target is positively labeled development: env is "dev"
//     and admin is not "owner". A production label, an owner label and a missing
//     label all give pinned.
//
// It returns the mode and which step decided, for the operator to read.
func ResolveAgentMode(hostMode, appDefault string, labels DeployLabels) (mode, why string) {
	mode, why, _ = resolveAgentMode(hostMode, appDefault, labels)
	return mode, why
}

// resolveAgentMode is ResolveAgentMode plus whether the answer is only the
// conservative fallback of step 3 (nothing said pinned, and the target is not
// positively dev), which an attended operator load may override.
func resolveAgentMode(hostMode, appDefault string, labels DeployLabels) (mode, why string, fallback bool) {
	switch hostMode {
	case "live", "pinned":
		return hostMode, "mode in host.yaml", false
	}
	switch appDefault {
	case "live", "pinned":
		return appDefault, "application default (" + EnvAgentMode + ")", false
	}
	if labels.Env == "dev" && labels.Admin != "owner" {
		return "live", "default for a target labeled env=dev", false
	}
	return "pinned", "default: the target is not labeled env=dev (set mode in host.yaml, or " + EnvAgentMode + ")", true
}
