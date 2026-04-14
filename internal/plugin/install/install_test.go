package install

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// --- fakes --------------------------------------------------------------

type fakeSource struct {
	id       string
	handle   Handle
	err      error
	gotStage string
}

func (f *fakeSource) PluginID() string { return f.id }
func (f *fakeSource) Download(ctx context.Context, stagingDir string, emit EventFunc) (Handle, error) {
	f.gotStage = stagingDir
	if f.err != nil {
		return Handle{}, f.err
	}
	return f.handle, nil
}

type fakeVerifier struct {
	err    error
	called bool
}

func (f *fakeVerifier) Verify(ctx context.Context, h Handle) error {
	f.called = true
	return f.err
}

type fakeExtractor struct {
	err    error
	called bool
}

func (f *fakeExtractor) Extract(ctx context.Context, h Handle, targetDir string, emit EventFunc) error {
	f.called = true
	return f.err
}

type fakeValidator struct {
	err    error
	called bool
}

func (f *fakeValidator) Validate(ctx context.Context, pluginDir string) error {
	f.called = true
	return f.err
}

type fakeLoader struct {
	err    error
	called bool
}

func (f *fakeLoader) Load(ctx context.Context, pluginID, pluginDir string) error {
	f.called = true
	return f.err
}

type fakeStaging struct {
	beginErr   error
	commitErr  error
	stagingDir string
	finalDir   string
	cleanups   int
	committed  bool
	mu         sync.Mutex
}

func (f *fakeStaging) Begin(ctx context.Context, pluginID string) (string, func(), error) {
	if f.beginErr != nil {
		return "", nil, f.beginErr
	}
	f.stagingDir = "/tmp/staging/" + pluginID
	return f.stagingDir, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.cleanups++
	}, nil
}

func (f *fakeStaging) Commit(ctx context.Context, stagingDir, pluginID string) (string, error) {
	if f.commitErr != nil {
		return "", f.commitErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.committed = true
	f.finalDir = "/tmp/plugins/" + pluginID
	return f.finalDir, nil
}

// --- helpers ------------------------------------------------------------

type recordedEvent struct {
	State State
	Err   error
}

func record() (EventFunc, *[]recordedEvent) {
	var mu sync.Mutex
	events := make([]recordedEvent, 0, 8)
	return func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, recordedEvent{e.State, e.Err})
	}, &events
}

func archiveHandle() Handle {
	return Handle{Kind: "archive", Path: "/tmp/staging/giphy/plugin.tar.gz"}
}

// --- tests --------------------------------------------------------------

func TestInstaller_HappyPath_Archive(t *testing.T) {
	src := &fakeSource{id: "giphy", handle: archiveHandle()}
	ver := &fakeVerifier{}
	ext := &fakeExtractor{}
	val := &fakeValidator{}
	ldr := &fakeLoader{}
	stg := &fakeStaging{}
	emit, events := record()

	i := &Installer{
		Verifier: ver, Extractor: ext, Validator: val, Loader: ldr,
		Staging: stg, Emit: emit,
	}

	final, err := i.Install(context.Background(), src)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if final != "/tmp/plugins/giphy" {
		t.Errorf("final dir = %q, want /tmp/plugins/giphy", final)
	}
	if !ver.called || !ext.called || !val.called || !ldr.called {
		t.Errorf("step not called: ver=%v ext=%v val=%v ldr=%v", ver.called, ext.called, val.called, ldr.called)
	}
	if !stg.committed {
		t.Error("staging not committed")
	}
	if stg.cleanups != 0 {
		t.Errorf("cleanup called %d times on happy path, want 0", stg.cleanups)
	}
	if got, want := i.State(), StateReady; got != want {
		t.Errorf("end state = %q, want %q", got, want)
	}

	wantStates := []State{StateDownloading, StateVerifying, StateExtracting, StateValidating, StateLoading, StateReady}
	if got := transitionStates(*events); !equalStates(got, wantStates) {
		t.Errorf("transitions = %v, want %v", got, wantStates)
	}
}

func TestInstaller_HappyPath_Directory_SkipsVerify(t *testing.T) {
	src := &fakeSource{id: "giphy", handle: Handle{Kind: "directory", Path: "/home/dev/giphy"}}
	ver := &fakeVerifier{}
	ext := &fakeExtractor{}
	val := &fakeValidator{}
	ldr := &fakeLoader{}
	stg := &fakeStaging{}

	i := &Installer{Verifier: ver, Extractor: ext, Validator: val, Loader: ldr, Staging: stg}
	if _, err := i.Install(context.Background(), src); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if ver.called {
		t.Error("verifier should not be called for directory handles")
	}
	if !ext.called {
		t.Error("extractor should still run (symlink placement) for directory handles")
	}
}

// Each table entry forces failure at one state and asserts:
// 1. Install returns the wrapped error.
// 2. State transitions into StateFailed.
// 3. Staging cleanup runs if failure occurred before Commit; does NOT run
//    after Commit (Loading failure).
// 4. Later steps are not invoked.
func TestInstaller_FailurePaths(t *testing.T) {
	cases := []struct {
		name            string
		mutate          func(src *fakeSource, ver *fakeVerifier, ext *fakeExtractor, val *fakeValidator, ldr *fakeLoader, stg *fakeStaging)
		failedIn        State
		wantCleanups    int
		wantVerCalled   bool
		wantExtCalled   bool
		wantValCalled   bool
		wantLoaderCalled bool
	}{
		{
			name:     "begin staging fails",
			mutate:   func(src *fakeSource, ver *fakeVerifier, ext *fakeExtractor, val *fakeValidator, ldr *fakeLoader, stg *fakeStaging) { stg.beginErr = errors.New("boom") },
			failedIn: StateNotInstalled,
			wantCleanups: 0,
		},
		{
			name:     "download fails",
			mutate:   func(src *fakeSource, ver *fakeVerifier, ext *fakeExtractor, val *fakeValidator, ldr *fakeLoader, stg *fakeStaging) { src.err = errors.New("net") },
			failedIn: StateDownloading,
			wantCleanups: 1,
		},
		{
			name:          "verify fails",
			mutate:        func(src *fakeSource, ver *fakeVerifier, ext *fakeExtractor, val *fakeValidator, ldr *fakeLoader, stg *fakeStaging) { ver.err = errors.New("bad sig") },
			failedIn:      StateVerifying,
			wantCleanups:  1,
			wantVerCalled: true,
		},
		{
			name:          "extract fails",
			mutate:        func(src *fakeSource, ver *fakeVerifier, ext *fakeExtractor, val *fakeValidator, ldr *fakeLoader, stg *fakeStaging) { ext.err = errors.New("tarslip") },
			failedIn:      StateExtracting,
			wantCleanups:  1,
			wantVerCalled: true,
			wantExtCalled: true,
		},
		{
			name:          "validate fails",
			mutate:        func(src *fakeSource, ver *fakeVerifier, ext *fakeExtractor, val *fakeValidator, ldr *fakeLoader, stg *fakeStaging) { val.err = errors.New("bad manifest") },
			failedIn:      StateValidating,
			wantCleanups:  1,
			wantVerCalled: true,
			wantExtCalled: true,
			wantValCalled: true,
		},
		{
			name:          "commit fails",
			mutate:        func(src *fakeSource, ver *fakeVerifier, ext *fakeExtractor, val *fakeValidator, ldr *fakeLoader, stg *fakeStaging) { stg.commitErr = errors.New("cross-fs") },
			failedIn:      StateValidating,
			wantCleanups:  1,
			wantVerCalled: true,
			wantExtCalled: true,
			wantValCalled: true,
		},
		{
			name:            "load fails after commit — no staging cleanup",
			mutate:          func(src *fakeSource, ver *fakeVerifier, ext *fakeExtractor, val *fakeValidator, ldr *fakeLoader, stg *fakeStaging) { ldr.err = errors.New("host refused") },
			failedIn:        StateLoading,
			wantCleanups:    0,
			wantVerCalled:   true,
			wantExtCalled:   true,
			wantValCalled:   true,
			wantLoaderCalled: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := &fakeSource{id: "giphy", handle: archiveHandle()}
			ver := &fakeVerifier{}
			ext := &fakeExtractor{}
			val := &fakeValidator{}
			ldr := &fakeLoader{}
			stg := &fakeStaging{}
			tc.mutate(src, ver, ext, val, ldr, stg)
			emit, events := record()
			i := &Installer{Verifier: ver, Extractor: ext, Validator: val, Loader: ldr, Staging: stg, Emit: emit}

			_, err := i.Install(context.Background(), src)
			if err == nil {
				t.Fatal("Install returned nil, want error")
			}
			if got := i.State(); got != StateFailed {
				t.Errorf("state = %q, want %q", got, StateFailed)
			}
			// Ensure a Failed event was emitted and names the right state.
			found := false
			for _, e := range *events {
				if e.State == StateFailed && e.Err != nil {
					found = true
				}
			}
			if !found {
				t.Error("no Failed event with err emitted")
			}
			// Cleanup count.
			stg.mu.Lock()
			got := stg.cleanups
			stg.mu.Unlock()
			if got != tc.wantCleanups {
				t.Errorf("cleanups = %d, want %d", got, tc.wantCleanups)
			}
			if ver.called != tc.wantVerCalled {
				t.Errorf("verifier called = %v, want %v", ver.called, tc.wantVerCalled)
			}
			if ext.called != tc.wantExtCalled {
				t.Errorf("extractor called = %v, want %v", ext.called, tc.wantExtCalled)
			}
			if val.called != tc.wantValCalled {
				t.Errorf("validator called = %v, want %v", val.called, tc.wantValCalled)
			}
			if ldr.called != tc.wantLoaderCalled {
				t.Errorf("loader called = %v, want %v", ldr.called, tc.wantLoaderCalled)
			}
		})
	}
}

func TestInstaller_GuardsMissingDeps(t *testing.T) {
	src := &fakeSource{id: "giphy", handle: archiveHandle()}

	cases := []struct {
		name string
		i    *Installer
	}{
		{"nil staging", &Installer{Validator: &fakeValidator{}, Loader: &fakeLoader{}}},
		{"nil validator", &Installer{Staging: &fakeStaging{}, Loader: &fakeLoader{}}},
		{"nil loader", &Installer{Staging: &fakeStaging{}, Validator: &fakeValidator{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.i.Install(context.Background(), src); err == nil {
				t.Fatal("want error for missing dep")
			}
		})
	}
}

func TestInstaller_NilSource(t *testing.T) {
	i := &Installer{Staging: &fakeStaging{}, Validator: &fakeValidator{}, Loader: &fakeLoader{}}
	if _, err := i.Install(context.Background(), nil); err == nil {
		t.Fatal("want error for nil source")
	}
}

func TestInstaller_EmptyPluginID(t *testing.T) {
	i := &Installer{Staging: &fakeStaging{}, Validator: &fakeValidator{}, Loader: &fakeLoader{}}
	if _, err := i.Install(context.Background(), &fakeSource{id: ""}); err == nil {
		t.Fatal("want error for empty plugin id")
	}
}

func TestInstaller_ArchiveRequiresVerifier(t *testing.T) {
	src := &fakeSource{id: "giphy", handle: archiveHandle()}
	i := &Installer{
		Staging:   &fakeStaging{},
		Extractor: &fakeExtractor{},
		Validator: &fakeValidator{},
		Loader:    &fakeLoader{},
	}
	_, err := i.Install(context.Background(), src)
	if err == nil {
		t.Fatal("want error when archive handle but Verifier nil")
	}
}

func TestInstaller_ExtractorRequired(t *testing.T) {
	src := &fakeSource{id: "giphy", handle: Handle{Kind: "directory", Path: "/d"}}
	i := &Installer{
		Staging:   &fakeStaging{},
		Validator: &fakeValidator{},
		Loader:    &fakeLoader{},
	}
	_, err := i.Install(context.Background(), src)
	if err == nil {
		t.Fatal("want error when Extractor nil")
	}
}

// --- small utilities ----------------------------------------------------

func transitionStates(events []recordedEvent) []State {
	var out []State
	for _, e := range events {
		out = append(out, e.State)
	}
	return out
}

func equalStates(a, b []State) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
