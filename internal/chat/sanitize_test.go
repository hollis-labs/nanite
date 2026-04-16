package chat

import (
	"strings"
	"testing"
)

func TestSanitizeToolError_Empty(t *testing.T) {
	if got := SanitizeToolError(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestSanitizeToolError_HomeDirReplacement(t *testing.T) {
	hd := getHomeDir()
	if hd == "" {
		t.Skip("no home dir")
	}
	input := "Error: file not found at " + hd + "/Projects/foo.go"
	got := SanitizeToolError(input)
	if strings.Contains(got, hd) {
		t.Errorf("home dir not replaced: %s", got)
	}
	if !strings.Contains(got, "~/Projects/foo.go") {
		t.Errorf("expected ~/Projects/foo.go, got: %s", got)
	}
}

func TestSanitizeToolError_UserPathRegex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "Users path",
			input: "open /Users/alice/secret/config.yaml: permission denied",
			want:  "open ~/secret/config.yaml: permission denied",
		},
		{
			name:  "home path",
			input: "open /home/bob/work/data.json: no such file",
			want:  "open ~/work/data.json: no such file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeToolError(tt.input)
			if !strings.Contains(got, tt.want) {
				t.Errorf("expected %q in output, got: %s", tt.want, got)
			}
		})
	}
}

func TestSanitizeToolError_SecretEnvLines(t *testing.T) {
	input := "OPENAI_API_KEY=sk-abc123\nPATH=/usr/bin\nDB_PASSWORD=hunter2\nnormal output"
	got := SanitizeToolError(input)
	if strings.Contains(got, "sk-abc123") {
		t.Error("API key not stripped")
	}
	if strings.Contains(got, "hunter2") {
		t.Error("password not stripped")
	}
	if !strings.Contains(got, "PATH=/usr/bin") {
		t.Error("non-secret env var should be preserved")
	}
	if !strings.Contains(got, "normal output") {
		t.Error("normal output should be preserved")
	}
}

func TestSanitizeToolError_SecretEnvSuffixes(t *testing.T) {
	for _, suffix := range []string{"_KEY", "_TOKEN", "_SECRET", "_PASS", "_PASSWORD"} {
		line := "MY" + suffix + "=sensitive_value"
		got := SanitizeToolError(line + "\nkeep this")
		if strings.Contains(got, "sensitive_value") {
			t.Errorf("suffix %s not stripped", suffix)
		}
		if !strings.Contains(got, "keep this") {
			t.Errorf("non-secret line removed for suffix %s", suffix)
		}
	}
}

func TestSanitizeToolError_GoStackTruncation(t *testing.T) {
	input := `some error happened
goroutine 1 [running]:
main.doSomething()
	/Users/alice/project/main.go:42 +0x1a2
main.caller()
	/Users/alice/project/main.go:10 +0x3b
main.main()
	/Users/alice/project/main.go:5 +0x25`

	got := SanitizeToolError(input)
	if !strings.Contains(got, "goroutine 1 [running]:") {
		t.Error("stack header should be preserved")
	}
	if !strings.Contains(got, "main.doSomething()") {
		t.Error("first frame function should be preserved")
	}
	if !strings.Contains(got, "[... stack truncated]") {
		t.Error("truncation sentinel missing")
	}
	// Second and third frames should be gone.
	if strings.Contains(got, "main.caller()") {
		t.Error("second frame should be truncated")
	}
	if strings.Contains(got, "main.main()") {
		t.Error("third frame should be truncated")
	}
}

func TestSanitizeToolError_ShortStack(t *testing.T) {
	// A stack with only one frame should not be truncated.
	input := "goroutine 1 [running]:\nmain.doSomething()\n\t/foo/bar.go:42"
	got := SanitizeToolError(input)
	if strings.Contains(got, "[... stack truncated]") {
		t.Error("single-frame stack should not be truncated")
	}
}

func TestSanitizeToolError_Composite(t *testing.T) {
	hd := getHomeDir()
	if hd == "" {
		t.Skip("no home dir")
	}
	input := "Error at " + hd + "/project/main.go\nANTHROPIC_API_KEY=sk-ant-123\ngoroutine 1 [running]:\nfoo()\n\tbar.go:1\nbaz()\n\tqux.go:2\nqux()\n\tquux.go:3"
	got := SanitizeToolError(input)
	if strings.Contains(got, hd) {
		t.Error("home dir leaked")
	}
	if strings.Contains(got, "sk-ant-123") {
		t.Error("API key leaked")
	}
	if !strings.Contains(got, "[... stack truncated]") {
		t.Error("stack not truncated")
	}
}
