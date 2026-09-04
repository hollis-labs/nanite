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
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	workflowModule       = "github.com/hollis-labs/go-workflow"
	workflowVersion      = "v0.1.0"
	hadronModule         = "github.com/hollis-labs/hadron"
	legacyWorkflowImport = "github.com/hollis-labs/nanite/internal/workflow"
)

func TestWorkflowDependencyBoundary(t *testing.T) {
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
	if violations := validateWorkflowModule(moduleData); len(violations) != 0 {
		t.Fatalf("workflow module policy failed:\n- %s", strings.Join(violations, "\n- "))
	}
	if _, statErr := fs.Stat(repositoryFS, "internal/workflow"); statErr == nil {
		t.Fatal("retired local workflow runtime directory internal/workflow still exists")
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("inspect retired local workflow runtime: %v", statErr)
	}
	for _, retired := range []string{
		"internal/agentworkflow/dag.go",
		"internal/service/workflow_engine.go",
		"internal/service/workflow_engine_flex.go",
		"internal/service/workflow_engine_loop.go",
	} {
		if _, statErr := fs.Stat(repositoryFS, retired); statErr == nil {
			t.Fatalf("retired local workflow sequencer %s still exists", retired)
		} else if !os.IsNotExist(statErr) {
			t.Fatalf("inspect retired workflow sequencer %s: %v", retired, statErr)
		}
	}
	if violations, scanErr := scanForbiddenWorkflowRuntimeDeclarations(repositoryFS); scanErr != nil {
		t.Fatal(scanErr)
	} else if len(violations) != 0 {
		t.Fatalf("local workflow runtime declarations returned:\n- %s", strings.Join(violations, "\n- "))
	}

	violations, workflowImports, err := scanGoImports(repositoryFS)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("workflow import boundary failed:\n- %s", strings.Join(violations, "\n- "))
	}
	if workflowImports == 0 {
		t.Fatal("workflow import boundary found no go-workflow consumers")
	}
}

func scanForbiddenWorkflowRuntimeDeclarations(repositoryFS fs.FS) ([]string, error) {
	var violations []string
	for _, directory := range []string{"internal/agentworkflow", "internal/service"} {
		walkErr := fs.WalkDir(repositoryFS, directory, func(filePath string, entry fs.DirEntry, visitErr error) error {
			if visitErr != nil {
				return visitErr
			}
			if entry.IsDir() || filepath.Ext(filePath) != ".go" || strings.HasSuffix(filePath, "_test.go") {
				return nil
			}
			data, err := fs.ReadFile(repositoryFS, filePath)
			if err != nil {
				return err
			}
			fileSet := token.NewFileSet()
			file, err := parser.ParseFile(fileSet, filePath, data, 0)
			if err != nil {
				return err
			}
			violations = append(violations, forbiddenRuntimeDeclarations(fileSet, filePath, file)...)
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}
	sort.Strings(violations)
	return violations, nil
}

// forbiddenRuntimeDeclarations detects the retired runtime by semantic
// declaration shape, not only by its historical identifiers. This prevents a
// local sequencer from returning under a cosmetic rename while leaving the
// product DTO and launcher boundary free to evolve.
func forbiddenRuntimeDeclarations(fileSet *token.FileSet, filePath string, file *ast.File) []string {
	var violations []string
	methodsByReceiver := make(map[string]map[string]*ast.FuncDecl)

	for _, declaration := range file.Decls {
		switch value := declaration.(type) {
		case *ast.GenDecl:
			for _, specification := range value.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if typeSpec.Name.Name == "WorkflowEngine" || typeSpec.Name.Name == "BuiltinWorkflowEngine" {
					violations = append(violations, declarationViolation(fileSet, filePath, typeSpec.Pos(), "retired workflow runtime type "+typeSpec.Name.Name))
				}
				interfaceType, ok := typeSpec.Type.(*ast.InterfaceType)
				if !ok {
					continue
				}
				methods := interfaceMethods(interfaceType)
				if isWorkflowRuntimeMethodSet(methods) && !isDurableWorkflowHostBoundary(filePath, typeSpec.Name.Name, methods) {
					violations = append(violations, declarationViolation(fileSet, filePath, typeSpec.Pos(), "workflow runtime interface shape "+typeSpec.Name.Name))
				}
			}
		case *ast.FuncDecl:
			if value.Name.Name == "NewBuiltinWorkflowEngine" || value.Name.Name == "Levels" {
				violations = append(violations, declarationViolation(fileSet, filePath, value.Pos(), "retired workflow runtime function "+value.Name.Name))
			}
			if value.Recv == nil {
				if isGraphSequencerSignature(value.Type) {
					violations = append(violations, declarationViolation(fileSet, filePath, value.Pos(), "workflow graph sequencing function shape "+value.Name.Name))
				}
				continue
			}
			receiver := receiverTypeName(value.Recv.List[0].Type)
			if receiver == "" {
				continue
			}
			if methodsByReceiver[receiver] == nil {
				methodsByReceiver[receiver] = make(map[string]*ast.FuncDecl)
			}
			methodsByReceiver[receiver][value.Name.Name] = value
		}
	}

	for receiver, methods := range methodsByReceiver {
		run := methods["Run"]
		if run == nil || !hasTypeIdentifier(run.Type.Params, "WorkflowDefinition") || !hasTypeIdentifier(run.Type.Results, "WorkflowResult") {
			continue
		}
		violations = append(violations, declarationViolation(fileSet, filePath, run.Pos(), "concrete workflow runtime method shape "+receiver+".Run"))
	}

	return violations
}

func interfaceMethods(value *ast.InterfaceType) map[string]*ast.FuncType {
	methods := make(map[string]*ast.FuncType)
	for _, field := range value.Methods.List {
		method, ok := field.Type.(*ast.FuncType)
		if !ok {
			continue
		}
		for _, name := range field.Names {
			methods[name.Name] = method
		}
	}
	return methods
}

func isWorkflowRuntimeMethodSet(methods map[string]*ast.FuncType) bool {
	run := methods["Run"]
	if run == nil || !hasTypeIdentifier(run.Params, "WorkflowDefinition") || !hasTypeIdentifier(run.Results, "WorkflowResult") {
		return false
	}
	return methods["Resume"] != nil || methods["ResumeGate"] != nil || methods["ResolveGate"] != nil || methods["Cancel"] != nil
}

func isDurableWorkflowHostBoundary(filePath, typeName string, methods map[string]*ast.FuncType) bool {
	if filePath != "internal/service/workflow_launch.go" || typeName != "DurableWorkflowHost" || len(methods) != 4 {
		return false
	}
	for _, name := range []string{"Run", "Resume", "ResumeGate", "Cancel"} {
		if methods[name] == nil {
			return false
		}
	}
	return true
}

func isGraphSequencerSignature(value *ast.FuncType) bool {
	if !hasTypeIdentifier(value.Params, "WorkflowDefinition") && !hasTypeIdentifier(value.Params, "StepDefinition") {
		return false
	}
	return hasNestedSliceOf(value.Results, "StepDefinition")
}

func hasTypeIdentifier(fields *ast.FieldList, target string) bool {
	if fields == nil {
		return false
	}
	found := false
	ast.Inspect(fields, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier.Name == target {
			found = true
			return false
		}
		return !found
	})
	return found
}

func hasNestedSliceOf(fields *ast.FieldList, target string) bool {
	if fields == nil {
		return false
	}
	for _, field := range fields.List {
		outer, ok := field.Type.(*ast.ArrayType)
		if !ok || outer.Len != nil {
			continue
		}
		inner, ok := outer.Elt.(*ast.ArrayType)
		if !ok || inner.Len != nil {
			continue
		}
		if hasIdentifier(inner.Elt, target) {
			return true
		}
	}
	return false
}

func hasIdentifier(expression ast.Expr, target string) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier.Name == target {
			found = true
			return false
		}
		return !found
	})
	return found
}

func receiverTypeName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return receiverTypeName(value.X)
	case *ast.IndexExpr:
		return receiverTypeName(value.X)
	case *ast.IndexListExpr:
		return receiverTypeName(value.X)
	default:
		return ""
	}
}

func declarationViolation(fileSet *token.FileSet, filePath string, position token.Pos, detail string) string {
	return fmt.Sprintf("%s:%d contains %s", filePath, fileSet.Position(position).Line, detail)
}

func TestWorkflowRuntimeDeclarationGuardRejectsRenamedRuntimeShapes(t *testing.T) {
	tests := map[string]string{
		"concrete runtime": `package service
type FlowCoordinator struct{}
func (*FlowCoordinator) Run(ctx Context, definition WorkflowDefinition, input WorkflowInput, executor StepExecutor) (WorkflowResult, error) { return WorkflowResult{}, nil }
`,
		"runtime interface": `package service
type FlowCoordinator interface {
	Run(Context, WorkflowDefinition, WorkflowInput, StepExecutor) (WorkflowResult, error)
	Resume(Context, string, StepExecutor) (WorkflowResult, error)
}
`,
		"graph sequencer": `package agentworkflow
func executionWaves(steps []StepDefinition) ([][]StepDefinition, error) { return nil, nil }
`,
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			fileSet := token.NewFileSet()
			file, err := parser.ParseFile(fileSet, name+".go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			if violations := forbiddenRuntimeDeclarations(fileSet, "internal/service/renamed.go", file); len(violations) == 0 {
				t.Fatal("renamed local workflow runtime declaration was accepted")
			}
		})
	}
}

func TestWorkflowRuntimeDeclarationGuardAllowsThinDurableHostBoundary(t *testing.T) {
	const source = `package service
type DurableWorkflowHost interface {
	Run(Context, WorkflowDefinition, WorkflowInput, StepExecutor) (WorkflowResult, error)
	Resume(Context, string, StepExecutor) (WorkflowResult, error)
	ResumeGate(Context, string, string, string, string, StepExecutor) (WorkflowResult, error)
	Cancel(Context, string, string) (WorkflowResult, error)
}
`
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "workflow_launch.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	if violations := forbiddenRuntimeDeclarations(fileSet, "internal/service/workflow_launch.go", file); len(violations) != 0 {
		t.Fatalf("thin durable host boundary rejected: %v", violations)
	}
}

func TestWorkflowModulePolicyRejectsDrift(t *testing.T) {
	tests := map[string]string{
		"latest":        "module example.com/host\nrequire " + workflowModule + " latest\n",
		"pseudo":        "module example.com/host\nrequire " + workflowModule + " v0.1.1-0.20260904000000-deadbeefdead\n",
		"wrong release": "module example.com/host\nrequire " + workflowModule + " v0.1.1\n",
		"replacement":   "module example.com/host\nrequire " + workflowModule + " " + workflowVersion + "\nreplace " + workflowModule + " => ../../libs/go-workflow\n",
		"Hadron":        "module example.com/host\nrequire (\n" + workflowModule + " " + workflowVersion + "\n" + hadronModule + " v0.5.0-beta.2\n)\n",
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			if violations := validateWorkflowModule([]byte(document)); len(violations) == 0 {
				t.Fatal("invalid workflow module policy was accepted")
			}
		})
	}
}

func validateWorkflowModule(data []byte) []string {
	var violations []string
	found := 0
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if comment := strings.Index(line, "//"); comment >= 0 {
			line = line[:comment]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "replace (" || strings.HasPrefix(line, "replace ") {
			violations = append(violations, "go.mod must not contain replace directives")
		}
		if strings.Contains(line, hadronModule) {
			violations = append(violations, "go.mod must not depend on the Hadron application module")
		}
		fields := strings.Fields(line)
		for index, field := range fields {
			if field != workflowModule {
				continue
			}
			found++
			if index+1 >= len(fields) || fields[index+1] != workflowVersion {
				version := "<missing>"
				if index+1 < len(fields) {
					version = fields[index+1]
				}
				violations = append(violations, fmt.Sprintf("go-workflow must resolve exactly to %s, got %s", workflowVersion, version))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		violations = append(violations, "read go.mod: "+err.Error())
	}
	if found != 1 {
		violations = append(violations, fmt.Sprintf("go.mod must contain exactly one go-workflow requirement, found %d", found))
	}
	sort.Strings(violations)
	return compactStrings(violations)
}

func scanGoImports(repositoryFS fs.FS) ([]string, int, error) {
	fset := token.NewFileSet()
	var violations []string
	workflowImports := 0
	err := fs.WalkDir(repositoryFS, ".", func(filePath string, entry fs.DirEntry, visitErr error) error {
		if visitErr != nil {
			return visitErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "exports", "node_modules", "vendor":
				if filePath != "." {
					return fs.SkipDir
				}
			}
			return nil
		}
		if filepath.Ext(filePath) != ".go" {
			return nil
		}
		data, err := fs.ReadFile(repositoryFS, filePath)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, filePath, data, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			switch {
			case pathWithin(imported, hadronModule):
				violations = append(violations, fmt.Sprintf("%s:%d imports Hadron application package %q", filePath, fset.Position(spec.Pos()).Line, imported))
			case pathWithin(imported, legacyWorkflowImport):
				violations = append(violations, fmt.Sprintf("%s:%d imports retired local workflow runtime %q", filePath, fset.Position(spec.Pos()).Line, imported))
			case pathWithin(imported, workflowModule):
				workflowImports++
			}
		}
		return nil
	})
	sort.Strings(violations)
	return violations, workflowImports, err
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture guard")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func pathWithin(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}
