package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestToolsPublishInstallCLI(t *testing.T) {
	t.Setenv("TETHER_AGENT_URN", "msg://agent/agent-mux/agt_fixture")
	src := t.TempDir()
	root, err := os.OpenRoot(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll("tools/echo", 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"identity.yaml": "principal: agent:fixture\n", "tools/echo/tool.yaml": "description: Echo\nversion: 1.0.0\nkind: command\nentry: run.sh\n", "tools/echo/run.sh": "#!/bin/sh\ncat\n"} {
		mode := os.FileMode(0600)
		if strings.HasSuffix(name, ".sh") {
			mode = 0700
		}
		if err := root.WriteFile(name, []byte(data), mode); err != nil {
			t.Fatal(err)
		}
	}
	registry := t.TempDir()
	var output bytes.Buffer
	publish := toolsCommand(src)
	publish.SetOut(&output)
	publish.SetArgs([]string{"publish", "echo", "--registry-dir", registry})
	if err := publish.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "tool-package:agt_fixture/echo@1.0.0 sha256:") {
		t.Fatal(output.String())
	}
	target := t.TempDir()
	install := toolsCommand(target)
	install.SetOut(&output)
	install.SetArgs([]string{"install", "tool-package:agt_fixture/echo@1.0.0", "--registry-dir", registry})
	if err := install.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "execution still requires") {
		t.Fatal(output.String())
	}
	for _, args := range [][]string{{"tools", "publish", "echo"}, {"tools", "install", "tool-package:agt_fixture/echo@1.0.0"}} {
		if !configIndependent(args) || requiresAgent(args) {
			t.Fatalf("tools CLI requires model/database configuration: %v", args)
		}
	}
	help := toolsCommand(target)
	output.Reset()
	help.SetOut(&output)
	help.SetArgs([]string{"--help"})
	if err := help.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Development-only") {
		t.Fatal("missing dev-only warning")
	}
}
