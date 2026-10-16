package service

import (
	"testing"

	permissionlib "github.com/hollis-labs/libs/security/policy/permission"
)

func TestResolveBootPermissionMode(t *testing.T) {
	for _, tc := range []struct {
		name, cfg, env string
		want           permissionlib.Mode
		wantSource     string
		wantErr        bool
	}{
		{name: "nothing set is default", want: permissionlib.ModeDefault, wantSource: "default"},
		{name: "config yolo", cfg: "yolo", want: permissionlib.ModeYolo, wantSource: "config"},
		{name: "env overrides config", cfg: "yolo", env: "plan", want: permissionlib.ModePlan, wantSource: "env"},
		{name: "env yolo, case and space", env: " YOLO ", want: permissionlib.ModeYolo, wantSource: "env"},
		{name: "accept-edits", cfg: "accept-edits", want: permissionlib.ModeAcceptEdits, wantSource: "config"},
		{name: "typo falls back to default and errors", cfg: "yoloo", want: permissionlib.ModeDefault, wantSource: "config", wantErr: true},
		{name: "bad env does not fall back to a yolo config", cfg: "yolo", env: "nope", want: permissionlib.ModeDefault, wantSource: "env", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, source, err := ResolveBootPermissionMode(tc.cfg, tc.env)
			if got != tc.want || source != tc.wantSource || (err != nil) != tc.wantErr {
				t.Fatalf("got (%q, %q, %v), want (%q, %q, err=%v)", got, source, err, tc.want, tc.wantSource, tc.wantErr)
			}
		})
	}
}
