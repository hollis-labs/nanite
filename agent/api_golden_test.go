package agent_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

var updateAPIGolden = flag.Bool("update-agent-api", false, "rewrite the exported agent API snapshot")

func TestAPIGolden(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "go", "list", "-export", "-deps", "-json", ".")
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v: %s", err, cmd.Stderr)
	}
	paths := make(map[string]string)
	var packagePath string
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var entry struct{ ImportPath, Export string }
		if err := decoder.Decode(&entry); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		paths[entry.ImportPath] = entry.Export
		packagePath = entry.ImportPath
	}
	compiled := importer.ForCompiler(token.NewFileSet(), "gc", func(path string) (io.ReadCloser, error) {
		return os.Open(paths[path])
	})
	pkg, err := compiled.Import(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	qualify := types.RelativeTo(pkg)
	for _, name := range pkg.Scope().Names() {
		obj := pkg.Scope().Lookup(name)
		if !obj.Exported() {
			continue
		}
		if _, ok := obj.(*types.TypeName); !ok {
			fmt.Fprintln(&got, types.ObjectString(obj, qualify))
			continue
		}
		fmt.Fprintf(&got, "type %s", name)
		typ := obj.Type()
		if alias, ok := typ.(*types.Alias); ok {
			fmt.Fprintf(&got, " = %s", types.TypeString(alias.Rhs(), qualify))
		}
		switch underlying := typ.Underlying().(type) {
		case *types.Struct:
			fmt.Fprintln(&got, " struct")
			for field := range underlying.Fields() {
				if field.Exported() {
					fmt.Fprintf(&got, "  %s\n", types.ObjectString(field, qualify))
				}
			}
		case *types.Interface:
			fmt.Fprintln(&got, " interface")
		default:
			fmt.Fprintf(&got, " %s\n", types.TypeString(underlying, qualify))
		}
		valueMethods := types.NewMethodSet(typ)
		allMethods := valueMethods
		if _, ok := typ.Underlying().(*types.Interface); !ok {
			allMethods = types.NewMethodSet(types.NewPointer(typ))
		}
		for method := range allMethods.Methods() {
			fn := method.Obj()
			if fn.Exported() {
				receiver := name
				if valueMethods.Lookup(fn.Pkg(), fn.Name()) == nil {
					receiver = "*" + receiver
				}
				signature := strings.TrimPrefix(types.TypeString(method.Type(), qualify), "func")
				fmt.Fprintf(&got, "  (%s) %s%s\n", receiver, fn.Name(), signature)
			}
		}
	}

	const path = "testdata/exported-api.golden"
	if *updateAPIGolden {
		if err := os.WriteFile(path, []byte(got.String()), 0600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != string(want) {
		t.Fatalf("exported agent API changed. Review with go test ./agent -run '^TestAPIGolden$' -update-agent-api and git diff -- agent/testdata/exported-api.golden\nActual API:\n%s", got.String())
	}
}
