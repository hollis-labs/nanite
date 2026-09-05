package architecture_test

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	agentWrapperModule    = "github.com/hollis-labs/go-agent-wrapper"
	agentWrapperVersion   = "v0.9.0"
	agentWrapperSum       = "h1:RPwtWZJgTQNAX9neWK1/5UJpgk3nIpPorgx1vjrpuGY="
	agentWrapperGoModSum  = "h1:cRkJmUNd39Jxg6mv28stv3BZUFwv4peWSlkcbY5uQ/U="
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
	for name, source := range map[string]string{
		"direct ACP client":  `package agent; import "github.com/hollis-labs/go-agent-wrapper/acp"; var _ acp.Client`,
		"direct process":     `package agent; import "os/exec"; func start(){ exec.Command("agent") }`,
		"direct start":       `package agent; import hostos "os"; func start(){ _, _ = hostos.StartProcess("agent", nil, nil) }`,
		"direct agentkit":    `package agent; import "github.com/hollis-labs/agentkit/agentsessions"; var _ = agentsessions.NewManager(nil)`,
		"direct ACP factory": `package agent; import c "github.com/hollis-labs/go-agent-wrapper/adapters/claudeacp"; var _ = c.NewClient()`,
		"dot ACP import":     `package agent; import . "github.com/hollis-labs/go-agent-wrapper/adapters/claudeacp"; var _ = NewClient()`,
		"shell projection":   "package agent; const script = \"#!/bin/sh\\nexec env -i foo\"",
		"local adapter":      `package agent; type nativeAdapter struct{}`,
	} {
		t.Run(name, func(t *testing.T) {
			fileSet := token.NewFileSet()
			file, err := parser.ParseFile(fileSet, "mutant.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			if violations := forbiddenAgentOwnership(fileSet, "mutant.go", file); len(violations) == 0 {
				t.Fatal("ownership bypass was accepted")
			}
		})
	}
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
	var violations []string
	err := fs.WalkDir(repositoryFS, "internal/runtime/agent", func(path string, entry fs.DirEntry, visitErr error) error {
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
		file, err := parser.ParseFile(fileSet, path, data, 0)
		if err != nil {
			return err
		}
		violations = append(violations, forbiddenAgentOwnership(fileSet, path, file)...)
		return nil
	})
	sort.Strings(violations)
	return violations, err
}

func forbiddenAgentOwnership(fileSet *token.FileSet, path string, file *ast.File) []string {
	var violations []string
	acpAliases := make(map[string]bool)
	clientAliases := make(map[string]bool)
	agentSessionAliases := make(map[string]bool)
	osAliases := make(map[string]bool)
	for _, imported := range file.Imports {
		importPath := strings.Trim(imported.Path.Value, `"`)
		alias := filepath.Base(importPath)
		if imported.Name != nil {
			alias = imported.Name.Name
		}
		if imported.Path.Value == `"os/exec"` {
			violations = append(violations, declarationViolation(fileSet, path, imported.Pos(), "direct os/exec import"))
		}
		if importPath == "syscall" {
			violations = append(violations, declarationViolation(fileSet, path, imported.Pos(), "direct syscall process ownership"))
		}
		if imported.Name != nil && imported.Name.Name == "." && (importPath == agentWrapperModule+"/acp" || strings.HasPrefix(importPath, agentWrapperModule+"/adapters/") || importPath == "github.com/hollis-labs/agentkit/agentsessions") {
			violations = append(violations, declarationViolation(fileSet, path, imported.Pos(), "dot import can bypass lifecycle ownership guard"))
		}
		if importPath == "os" {
			osAliases[alias] = true
		}
		if importPath == agentWrapperModule+"/acp" {
			acpAliases[alias] = true
		}
		if strings.HasPrefix(importPath, agentWrapperModule+"/adapters/") && strings.HasSuffix(importPath, "acp") {
			clientAliases[alias] = true
		}
		if importPath == "github.com/hollis-labs/agentkit/agentsessions" {
			agentSessionAliases[alias] = true
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.TypeSpec:
			switch value.Name.Name {
			case "nativeAdapter", "envWrappedCLIAdapter", "acpSession":
				violations = append(violations, declarationViolation(fileSet, path, value.Pos(), "retired local lifecycle type "+value.Name.Name))
			}
		case *ast.SelectorExpr:
			ident, ok := value.X.(*ast.Ident)
			if !ok {
				break
			}
			if acpAliases[ident.Name] && value.Sel.Name == "Client" {
				violations = append(violations, declarationViolation(fileSet, path, value.Pos(), "direct acp.Client ownership"))
			}
			if clientAliases[ident.Name] && value.Sel.Name == "NewClient" {
				violations = append(violations, declarationViolation(fileSet, path, value.Pos(), "direct ACP client factory"))
			}
			if agentSessionAliases[ident.Name] && (value.Sel.Name == "NewManager" || value.Sel.Name == "NewFromAdapter") {
				violations = append(violations, declarationViolation(fileSet, path, value.Pos(), "direct agentkit session lifecycle"))
			}
			if osAliases[ident.Name] && value.Sel.Name == "StartProcess" {
				violations = append(violations, declarationViolation(fileSet, path, value.Pos(), "direct os.StartProcess ownership"))
			}
		case *ast.BasicLit:
			if value.Kind == token.STRING && (strings.Contains(value.Value, "env -i") || strings.Contains(value.Value, "#!/bin/sh")) {
				violations = append(violations, declarationViolation(fileSet, path, value.Pos(), "generated shell process projection"))
			}
		}
		return true
	})
	return violations
}
