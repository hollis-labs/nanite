package main

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SchemaV1 is the only deploy.yaml schema this tool understands.
const SchemaV1 = "fleet-deploy/v1"

var (
	nameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	versionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
	tmplVarRe = regexp.MustCompile(`\{([a-z_0-9]+)\}`)
)

type Spec struct {
	Schema   string   `yaml:"schema"`
	App      string   `yaml:"app"`
	Repo     string   `yaml:"repo"`
	Artifact Artifact `yaml:"artifact"`
	Targets  []Target `yaml:"targets"`
}

type Artifact struct {
	Name    string `yaml:"name"`
	Tarball string `yaml:"tarball"`
	Sums    string `yaml:"sums"`
}

type When struct {
	OS   string `yaml:"os"`
	Arch string `yaml:"arch"`
}

type Target struct {
	ID          string      `yaml:"id"`
	When        When        `yaml:"when"`
	Hosts       []string    `yaml:"hosts"`
	Binary      string      `yaml:"binary"`
	InstallRoot string      `yaml:"install_root"`
	BackupDir   string      `yaml:"backup_dir"`
	Symlinks    []Symlink   `yaml:"symlinks"`
	UnitDropins []Dropin    `yaml:"unit_dropins"`
	Preflight   []Preflight `yaml:"preflight"`
	Restart     RestartList `yaml:"restart"`
	Health      HealthList  `yaml:"health"`
}

type Symlink struct {
	Path string `yaml:"path"`
}

type Dropin struct {
	Unit    string `yaml:"unit"`
	Name    string `yaml:"name"`
	Content string `yaml:"content"`
}

type Preflight struct {
	Kind string `yaml:"kind"`
	DB   string `yaml:"db"`
	SQL  string `yaml:"sql"`
	Dest string `yaml:"dest"`
	Path string `yaml:"path"`
}

type Restart struct {
	Via      string   `yaml:"via"`
	Unit     string   `yaml:"unit"`
	Resource string   `yaml:"resource"`
	Verb     string   `yaml:"verb"`
	Run      bool     `yaml:"run"`
	Argv     []string `yaml:"argv"`
}

type Health struct {
	URL          string `yaml:"url"`
	Method       string `yaml:"method"`
	ExpectStatus []int  `yaml:"expect_status"`
	Timeout      string `yaml:"timeout"`
	Interval     string `yaml:"interval"`
	VersionJSON  string `yaml:"version_json"`
}

// Durations returns the parsed timeout and interval with defaults applied.
func (h Health) Durations() (timeout, interval time.Duration) {
	timeout, interval = 30*time.Second, time.Second
	if d, err := time.ParseDuration(h.Timeout); err == nil && h.Timeout != "" {
		timeout = d
	}
	if d, err := time.ParseDuration(h.Interval); err == nil && h.Interval != "" {
		interval = d
	}
	return
}

// RestartList and HealthList accept a single mapping or a list in deploy.yaml.
type RestartList []Restart
type HealthList []Health

func (l *RestartList) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeOneOrMany[Restart](n)
	*l = v
	return err
}

func (l *HealthList) UnmarshalYAML(n *yaml.Node) error {
	v, err := decodeOneOrMany[Health](n)
	*l = v
	return err
}

func decodeOneOrMany[T any](n *yaml.Node) ([]T, error) {
	if n.Kind == yaml.SequenceNode {
		var out []T
		err := n.Decode(&out)
		return out, err
	}
	var one T
	if err := n.Decode(&one); err != nil {
		return nil, err
	}
	return []T{one}, nil
}

// ParseSpec decodes strictly (unknown fields are errors) and validates deploy.yaml.
func ParseSpec(data []byte) (*Spec, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var s Spec
	if err := dec.Decode(&s); err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("deploy.yaml is empty")
		}
		return nil, fmt.Errorf("deploy.yaml: %v", err)
	}
	if errs := s.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("deploy.yaml invalid:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return &s, nil
}

var (
	preflightKinds = map[string]bool{"sql_zero": true, "sqlite_backup": true, "migration_check": true, "file_backup": true}
	restartVias    = map[string]bool{"systemd-user": true, "cerberus": true, "command": true, "none": true}
	cerberusVerbs  = map[string]bool{"reload": true, "restart": true, "apply": true}
	tmplVars       = map[string]bool{"backup_dir": true, "version": true, "sha12": true, "app": true, "os": true, "arch": true, "install_dir": true}
)

// Validate returns every problem found, each with its location.
func (s *Spec) Validate() []string {
	var e []string
	add := func(f string, a ...any) { e = append(e, fmt.Sprintf(f, a...)) }
	if s.Schema != SchemaV1 {
		add("schema: unsupported %q (this fleet-install supports %q)", s.Schema, SchemaV1)
	}
	if !nameRe.MatchString(s.App) {
		add("app: %q is missing or not a valid app name", s.App)
	}
	if s.Repo == "" {
		add("repo: required (owner/name)")
	}
	if s.Artifact.Tarball == "" {
		add("artifact.tarball: required, e.g. app_{version}_{os}_{arch}.tar.gz")
	} else if !strings.Contains(s.Artifact.Tarball, "{version}") {
		add("artifact.tarball: must contain {version}")
	}
	if len(s.Targets) == 0 {
		add("targets: at least one target is required")
	}
	seen := map[string]bool{}
	for i, t := range s.Targets {
		p := fmt.Sprintf("targets[%d]", i)
		if t.ID != "" {
			p += "(" + t.ID + ")"
		}
		if !nameRe.MatchString(t.ID) {
			add("%s.id: %q is missing or invalid", p, t.ID)
		} else if seen[t.ID] {
			add("%s.id: duplicate target id", p)
		}
		seen[t.ID] = true
		if t.When.OS == "" || t.When.Arch == "" {
			add("%s.when: os and arch are required", p)
		}
		if t.Binary == "" || t.Binary != filepath.Base(t.Binary) || t.Binary == "." || t.Binary == ".." {
			add("%s.binary: %q must be a plain file name inside the tarball", p, t.Binary)
		}
		if t.InstallRoot == "" || !(strings.HasPrefix(t.InstallRoot, "/") || strings.HasPrefix(t.InstallRoot, "~/")) {
			add("%s.install_root: %q must be absolute (or start with ~/)", p, t.InstallRoot)
		}
		if len(t.Symlinks) == 0 {
			add("%s.symlinks: at least one symlink path is required", p)
		}
		for j, l := range t.Symlinks {
			if !(strings.HasPrefix(l.Path, "/") || strings.HasPrefix(l.Path, "~/")) {
				add("%s.symlinks[%d].path: %q must be absolute (or start with ~/)", p, j, l.Path)
			}
		}
		for j, d := range t.UnitDropins {
			q := fmt.Sprintf("%s.unit_dropins[%d]", p, j)
			if d.Unit == "" || strings.ContainsAny(d.Unit, "/\\") {
				add("%s.unit: %q is missing or contains a path separator", q, d.Unit)
			}
			if d.Name == "" || strings.ContainsAny(d.Name, "/\\") {
				add("%s.name: %q is missing or contains a path separator", q, d.Name)
			}
			if d.Content == "" {
				add("%s.content: required", q)
			}
		}
		for j, pf := range t.Preflight {
			q := fmt.Sprintf("%s.preflight[%d]", p, j)
			if !preflightKinds[pf.Kind] {
				add("%s.kind: unknown kind %q (known: sql_zero, sqlite_backup, migration_check, file_backup)", q, pf.Kind)
				continue
			}
			switch pf.Kind {
			case "sql_zero":
				if pf.DB == "" || pf.SQL == "" {
					add("%s: sql_zero requires db and sql", q)
				}
			case "sqlite_backup":
				if pf.DB == "" || pf.Dest == "" {
					add("%s: sqlite_backup requires db and dest", q)
				}
			case "migration_check":
				if pf.DB == "" {
					add("%s: migration_check requires db", q)
				}
			case "file_backup":
				if pf.Path == "" || pf.Dest == "" {
					add("%s: file_backup requires path and dest", q)
				}
			}
			for _, v := range tmplVarRe.FindAllStringSubmatch(pf.Dest, -1) {
				if !tmplVars[v[1]] {
					add("%s.dest: unknown template variable {%s}", q, v[1])
				}
			}
		}
		for j, r := range t.Restart {
			q := fmt.Sprintf("%s.restart[%d]", p, j)
			if !restartVias[r.Via] {
				add("%s.via: unknown mode %q (known: systemd-user, cerberus, command, none)", q, r.Via)
				continue
			}
			switch r.Via {
			case "systemd-user":
				if r.Unit == "" {
					add("%s: systemd-user requires unit", q)
				}
			case "cerberus":
				if r.Resource == "" {
					add("%s: cerberus requires resource", q)
				}
				if r.Verb != "" && !cerberusVerbs[r.Verb] {
					add("%s.verb: %q not allowed (reload, restart, apply)", q, r.Verb)
				}
			case "command":
				if len(r.Argv) == 0 {
					add("%s: command requires a non-empty argv list", q)
				}
			}
		}
		for j, h := range t.Health {
			q := fmt.Sprintf("%s.health[%d]", p, j)
			if h.URL == "" {
				add("%s.url: required", q)
			}
			if h.Method != "" && h.Method != "GET" && h.Method != "HEAD" && h.Method != "POST" {
				add("%s.method: %q not allowed (GET, HEAD, POST)", q, h.Method)
			}
			for _, c := range h.ExpectStatus {
				if c < 100 || c > 599 {
					add("%s.expect_status: %d is not an HTTP status", q, c)
				}
			}
			for _, d := range []string{h.Timeout, h.Interval} {
				if d != "" {
					if _, err := time.ParseDuration(d); err != nil {
						add("%s: bad duration %q", q, d)
					}
				}
			}
		}
	}
	return e
}

// ExpandTemplate substitutes {name} variables; unknown ones are left in place.
func ExpandTemplate(s string, vars map[string]string) string {
	return tmplVarRe.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := vars[m[1:len(m)-1]]; ok {
			return v
		}
		return m
	})
}
