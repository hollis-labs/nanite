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
	"strconv"
	"strings"
	"testing"
)

const (
	messagingModule       = "github.com/hollis-labs/go-messaging"
	messagingVersion      = "v0.5.2"
	mailboxImport         = messagingModule + "/mailbox"
	legacyMessagingImport = "github.com/hollis-labs/nanite/internal/messaging"
	messagingModuleSum    = "h1:sMVnZRE1BAm/AN+36Zri5Bdd4SzF+EmCqULL627ztXA="
	messagingGoModSum     = "h1:hSEDlxOWkgZQAxlqFDfKvQ1hSHNZ8zrwee+0rAKuvXc="
)

func TestMessagingDependencyBoundary(t *testing.T) {
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
	if violations := validateMessagingModule(moduleData); len(violations) != 0 {
		t.Fatalf("messaging module policy failed:\n- %s", strings.Join(violations, "\n- "))
	}
	sumData, err := fs.ReadFile(repositoryFS, "go.sum")
	if err != nil {
		t.Fatal(err)
	}
	if violations := validateMessagingSums(sumData); len(violations) != 0 {
		t.Fatalf("messaging module sums failed:\n- %s", strings.Join(violations, "\n- "))
	}
	if _, statErr := fs.Stat(repositoryFS, "internal/messaging"); statErr == nil {
		t.Fatal("retired local mailbox directory internal/messaging still exists")
	} else if !os.IsNotExist(statErr) {
		t.Fatalf("inspect retired local mailbox directory: %v", statErr)
	}

	violations, productionImports, err := scanMessagingBoundary(repositoryFS)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("messaging dependency boundary failed:\n- %s", strings.Join(violations, "\n- "))
	}
	if productionImports == 0 {
		t.Fatal("messaging boundary found no production go-messaging/mailbox consumers")
	}
}

func TestMessagingDependencyValidatorsRejectMutants(t *testing.T) {
	t.Run("wrong module version", func(t *testing.T) {
		module := []byte("module example.com/host\nrequire " + messagingModule + " v0.3.0\n")
		if violations := validateMessagingModule(module); len(violations) == 0 {
			t.Fatal("wrong go-messaging version was accepted")
		}
	})
	t.Run("module replace", func(t *testing.T) {
		module := []byte("module example.com/host\nrequire " + messagingModule + " " + messagingVersion + "\nreplace " + messagingModule + " => ../go-messaging\n")
		if violations := validateMessagingModule(module); len(violations) == 0 {
			t.Fatal("go-messaging replace was accepted")
		}
	})
	t.Run("wrong module sum", func(t *testing.T) {
		sums := []byte(messagingModule + " " + messagingVersion + " h1:wrong\n" +
			messagingModule + " " + messagingVersion + "/go.mod " + messagingGoModSum + "\n")
		if violations := validateMessagingSums(sums); len(violations) == 0 {
			t.Fatal("wrong go-messaging checksum was accepted")
		}
	})
	t.Run("renamed message copy", func(t *testing.T) {
		assertCopiedMailboxDeclaration(t, `package mutant
			type LocalLetter struct {
				FromSessionID, FromAgentID, ToSessionID, ToAgentID string
				ThreadID, PayloadJSON string
			}`)
	})
	t.Run("renamed store copy", func(t *testing.T) {
		assertCopiedMailboxDeclaration(t, `package mutant
			type LocalStore interface {
				Send(); Get(); Inbox(); Thread(); Recent(); Ack(); Resolve(); UnreadCount()
			}`)
	})
}

func assertCopiedMailboxDeclaration(t *testing.T, source string) {
	t.Helper()
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "mutant.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	if violations := copiedMailboxDeclarations(fileSet, "mutant.go", file); len(violations) == 0 {
		t.Fatal("local mailbox primitive copy was accepted")
	}
}

func validateMessagingSums(data []byte) []string {
	want := map[string]string{
		messagingModule + " " + messagingVersion:             messagingModuleSum,
		messagingModule + " " + messagingVersion + "/go.mod": messagingGoModSum,
	}
	found := make(map[string]int)
	var violations []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 || fields[0] != messagingModule {
			continue
		}
		key := fields[0] + " " + fields[1]
		expected, ok := want[key]
		if !ok {
			violations = append(violations, "go.sum contains unexpected go-messaging version "+fields[1])
			continue
		}
		found[key]++
		if fields[2] != expected {
			violations = append(violations, fmt.Sprintf("%s checksum = %s, want %s", key, fields[2], expected))
		}
	}
	if err := scanner.Err(); err != nil {
		violations = append(violations, "read go.sum: "+err.Error())
	}
	for key := range want {
		if found[key] != 1 {
			violations = append(violations, fmt.Sprintf("go.sum must contain %s exactly once, found %d", key, found[key]))
		}
	}
	sort.Strings(violations)
	return compactStrings(violations)
}

func validateMessagingModule(data []byte) []string {
	var violations []string
	found := 0
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
			for _, field := range fields {
				if pathWithin(field, messagingModule) {
					violations = append(violations, "go.mod must not replace go-messaging")
				}
			}
		}
		for index, field := range fields {
			if field != messagingModule {
				continue
			}
			found++
			version := "<missing>"
			if index+1 < len(fields) {
				version = fields[index+1]
			}
			if version != messagingVersion {
				violations = append(violations, fmt.Sprintf("go-messaging must resolve exactly to %s, got %s", messagingVersion, version))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		violations = append(violations, "read go.mod: "+err.Error())
	}
	if found != 1 {
		violations = append(violations, fmt.Sprintf("go.mod must contain exactly one go-messaging requirement, found %d", found))
	}
	sort.Strings(violations)
	return compactStrings(violations)
}

func scanMessagingBoundary(repositoryFS fs.FS) ([]string, int, error) {
	fileSet := token.NewFileSet()
	var violations []string
	productionImports := 0
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
		file, err := parser.ParseFile(fileSet, filePath, data, 0)
		if err != nil {
			return err
		}
		isTest := strings.HasSuffix(filePath, "_test.go")
		for _, specification := range file.Imports {
			imported, err := strconv.Unquote(specification.Path.Value)
			if err != nil {
				return err
			}
			if pathWithin(imported, legacyMessagingImport) {
				violations = append(violations, positionViolation(fileSet, filePath, specification.Pos(), "imports retired local messaging package"))
			}
			if !isTest && imported == mailboxImport {
				productionImports++
			}
		}
		if !isTest {
			violations = append(violations, copiedMailboxDeclarations(fileSet, filePath, file)...)
		}
		return nil
	})
	sort.Strings(violations)
	return violations, productionImports, err
}

func copiedMailboxDeclarations(fileSet *token.FileSet, filePath string, file *ast.File) []string {
	var violations []string
	forbiddenTypeNames := map[string]struct{}{
		"CallerIdentity": {}, "InboxFilter": {}, "SQLiteStore": {},
		"NotificationSink": {}, "WakeReactor": {}, "EventStore": {},
		"HandoffCoordinator": {}, "SendInput": {},
	}
	forbiddenFunctionNames := map[string]struct{}{
		"NewSQLiteStore": {}, "WithCaller": {}, "CallerFromCtx": {}, "ValidateAgentID": {},
	}
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Recv == nil {
			if _, forbidden := forbiddenFunctionNames[function.Name.Name]; forbidden {
				violations = append(violations, positionViolation(fileSet, filePath, function.Pos(), "redeclares mailbox function "+function.Name.Name))
			}
			continue
		}
		general, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if _, forbidden := forbiddenTypeNames[typeSpec.Name.Name]; forbidden {
				violations = append(violations, positionViolation(fileSet, filePath, typeSpec.Pos(), "redeclares mailbox type "+typeSpec.Name.Name))
				continue
			}
			switch value := typeSpec.Type.(type) {
			case *ast.StructType:
				fields := structFieldNames(value)
				if containsAll(fields, "FromSessionID", "FromAgentID", "ToSessionID", "ToAgentID", "ThreadID", "PayloadJSON") {
					violations = append(violations, positionViolation(fileSet, filePath, typeSpec.Pos(), "declares a local copy of mailbox.Message/SendInput"))
				}
			case *ast.InterfaceType:
				methods := interfaceMethodNames(value)
				if containsAll(methods, "Send", "Get", "Inbox", "Thread", "Recent", "Ack", "Resolve", "UnreadCount") {
					violations = append(violations, positionViolation(fileSet, filePath, typeSpec.Pos(), "declares a local copy of mailbox.Store"))
				}
			}
		}
	}
	return violations
}

func structFieldNames(value *ast.StructType) map[string]struct{} {
	result := make(map[string]struct{})
	for _, field := range value.Fields.List {
		for _, name := range field.Names {
			result[name.Name] = struct{}{}
		}
	}
	return result
}

func interfaceMethodNames(value *ast.InterfaceType) map[string]struct{} {
	result := make(map[string]struct{})
	for _, field := range value.Methods.List {
		for _, name := range field.Names {
			result[name.Name] = struct{}{}
		}
	}
	return result
}

func containsAll(values map[string]struct{}, names ...string) bool {
	for _, name := range names {
		if _, ok := values[name]; !ok {
			return false
		}
	}
	return true
}

func positionViolation(fileSet *token.FileSet, filePath string, position token.Pos, message string) string {
	return fmt.Sprintf("%s:%d %s", filePath, fileSet.Position(position).Line, message)
}
