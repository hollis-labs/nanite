//go:build linux

package sandbox

// AD-02 (TASKS/audit-remediation/ARCHITECT-DECISIONS.md, decided
// 2026-08-22) implements this package's own long-standing
// TODO(network-isolation): move the allowlist Proxy inside the sandbox's
// own network namespace so os_linux.go's --unshare-net can be
// unconditional, instead of keeping the sandboxed process in the HOST's
// network namespace whenever NetworkAllow is non-empty (which made a
// configured allowlist strictly WEAKER than no allowlist at all — see
// os_linux.go's doc comment).
//
// Mechanism (mirrors a pattern already built and integration-tested in the
// sibling github.com/hollis-labs/go-sandbox module's loopback-forwarder,
// itself originally extracted from this file's own earlier hardening
// work — see that module's sandbox/apply_linux.go and
// loopback_helper_linux.go doc comments):
//
//  1. bwrap's --unshare-net gives the sandboxed process a fresh, empty
//     network namespace containing only a `lo` device — present, but
//     administratively DOWN. Nothing routes in or out.
//  2. Instead of wrapping the caller's real command directly, bwrap wraps
//     a trampoline re-exec of the current Nanite binary itself. Because
//     bwrap always execs exactly one payload, and namespace membership is
//     a property of the calling process (inherited across exec, not
//     re-derived from argv[0]), the trampoline runs INSIDE the freshly
//     unshared namespaces. This file's init() catches that re-exec (via a
//     private env var + argv marker) before Nanite's own main() ever
//     runs, so no other Nanite subsystem sees or is affected by it.
//  3. The trampoline (runNetnsHelper) brings `lo` up via a raw ioctl (no
//     `ip` binary needed inside the narrow bwrap root), then spawns a
//     second re-exec of itself — the "supervisor" (runNetnsSupervisor) —
//     as a genuine child process (fork+exec via os/exec, not a bare
//     fork(), which is unsafe in a multi-threaded Go runtime). The
//     supervisor is tied to the helper's lifetime via Pdeathsig so it
//     cannot outlive the sandboxed command.
//  4. The supervisor listens on 127.0.0.1:<proxyPort> — the EXACT port
//     AgentExec's HTTP_PROXY/HTTPS_PROXY env vars already point at — but
//     now bound inside the sandbox's own namespace, so the env vars work
//     unmodified. Each accepted connection is relayed byte-for-byte to a
//     Unix-domain socket at a short, fixed-root path (netnsBridgeRoot,
//     below — deliberately NOT nested under the caller's own sandboxDir;
//     see that constant's doc comment for why), bind-mounted into the
//     sandbox by an explicit bwrap flag this file adds (extraBwrapArgs).
//  5. Meanwhile the helper execs the ORIGINAL wrapped command (the
//     agent's actual shell/script), replacing itself — the supervisor
//     keeps running as its sibling for the lifetime of the sandboxed
//     command.
//  6. On the HOST side (newNetnsBridge, called from applyOSSandbox before
//     bwrap even starts), a listener on that same Unix-domain socket path
//     accepts each relayed connection and dials straight through to
//     127.0.0.1:<proxyPort> in the HOST's OWN, unmodified network
//     namespace — exactly where the existing, unmodified Proxy (proxy.go)
//     is already listening. Proxy's domain-allowlist / SSRF-guard logic
//     is untouched by any of this; it never learns the request originated
//     across a namespace boundary.
//
// Net effect: the sandboxed process's own network stack has a route to
// nothing but its own (now-forwarded) loopback. The only path to the real
// network is the byte-for-byte relay above, which always lands on the
// same allowlist-checked Proxy that existed before this change — no raw
// socket or protocol the sandboxed process could open reaches anything
// else. A caller with NO allowlist configured (proxyAddr == "" or
// networkAllow empty) never engages any of this — applyOSSandbox already
// unshares the network namespace unconditionally in that case and there
// is nothing to bridge.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/hollis-labs/nanite/internal/safego"
)

const (
	// netnsHelperEnvVar gates the init()-time trampoline catch below.
	// Set only on the cmd.Env of a bwrap-wrapped payload this package
	// itself constructs — never present in a normal `nanite` invocation.
	netnsHelperEnvVar = "__NANITE_SANDBOX_NETNS_HELPER"
	// netnsHelperArg / netnsSupervisorArg are the argv[1] markers
	// distinguishing the two re-exec identities (see package doc above).
	netnsHelperArg     = "__nanite_sandbox_netns_helper__"
	netnsSupervisorArg = "__nanite_sandbox_netns_supervisor__"
	// netnsBridgeDirEnvVar carries the bind-mounted, sandbox-visible
	// directory containing the Unix-domain relay socket.
	netnsBridgeDirEnvVar = "__NANITE_SANDBOX_NETNS_BRIDGE_DIR"
	// netnsForwardPortEnvVar carries the host allowlist Proxy's port —
	// the same port AgentExec's HTTP_PROXY/HTTPS_PROXY env vars use.
	netnsForwardPortEnvVar = "__NANITE_SANDBOX_NETNS_FORWARD_PORT"
	// netnsBridgeSocketName is the fixed filename for the Unix-domain
	// relay socket inside the per-invocation bridge dir. One bridge dir
	// per AgentExec call (see newNetnsBridge), so a fixed name is fine.
	netnsBridgeSocketName = "netns-bridge.sock"
)

// init catches the re-exec trampoline before Nanite's own main() runs.
// Mirrors the exact safety pattern used by the sibling go-sandbox module:
// gated behind both a private env var AND an argv[1] marker so an
// ordinary `nanite ...` invocation can never accidentally match, and the
// overhead for every normal invocation is one os.Getenv call.
func init() {
	if os.Getenv(netnsHelperEnvVar) != "1" {
		return
	}
	switch {
	case len(os.Args) >= 2 && os.Args[1] == netnsHelperArg:
		os.Exit(runNetnsHelper())
	case len(os.Args) >= 2 && os.Args[1] == netnsSupervisorArg:
		os.Exit(runNetnsSupervisor())
	}
}

// --- host side: wires the bridge before bwrap starts -----------------

// netnsBridge holds the host-side listener plus the payload rewrite
// applyOSSandbox needs to route the sandboxed process through the
// trampoline instead of exec'ing the caller's command directly.
type netnsBridge struct {
	dir      string
	listener net.Listener

	payloadPath string
	payloadArgs []string
	env         []string
}

// netnsBridgeRoot is the parent directory for per-invocation bridge dirs.
// Deliberately NOT nested under the caller's sandboxDir (which is rooted
// under $HOME and includes a caller-supplied session ID up to 64 bytes,
// see sessionIDRe in sandbox.go): AF_UNIX socket paths are capped at 108
// bytes by the kernel (sizeof sun_path), and $HOME + ".nanite/sandboxes/"
// + a long session ID + ".sandbox/netns-bridge-<rand>/netns-bridge.sock"
// can realistically exceed that on hosts with a deep home directory —
// confirmed directly during this task's real-Linux verification (a long
// Go test name via t.TempDir() hit the exact same 108-byte ceiling: "bind:
// invalid argument"). Using a short, fixed root keeps the full path well
// under the limit regardless of sandboxDir's length, at the cost of one
// extra explicit --ro-bind (see extraBwrapArgs) instead of reusing
// sandboxDir's existing bind.
const netnsBridgeRoot = "/tmp"

// newNetnsBridge sets up the host side of the AD-02 relay: a short-lived
// directory under netnsBridgeRoot holding a Unix-domain socket, a
// goroutine relaying accepted connections to the real allowlist Proxy on
// the host's own loopback, and the rewritten payload (trampoline binary +
// marker args) applyOSSandbox should wrap with bwrap instead of the
// original command.
func newNetnsBridge(proxyAddr, origPath string, origArgs, env []string) (*netnsBridge, error) {
	_, portStr, err := net.SplitHostPort(proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("parse proxy addr %q: %w", proxyAddr, err)
	}

	dir, err := os.MkdirTemp(netnsBridgeRoot, "nanite-netns-bridge-*")
	if err != nil {
		return nil, fmt.Errorf("create netns bridge dir: %w", err)
	}

	socketPath := filepath.Join(dir, netnsBridgeSocketName)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("listen on netns bridge socket: %w", err)
	}

	safego.Go(context.Background(), "sandbox.netns-bridge.host-serve", func() {
		serveNetnsBridgeHostSide(listener, proxyAddr)
	})

	helperPath, err := os.Executable()
	if err != nil {
		_ = listener.Close()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("resolve nanite executable: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(helperPath); resolveErr == nil {
		helperPath = resolved
	}

	resolvedOrigPath := origPath
	if !filepath.IsAbs(resolvedOrigPath) {
		if looked, lookErr := exec.LookPath(origPath); lookErr == nil {
			resolvedOrigPath = looked
		}
	}

	newEnv := append(append([]string(nil), env...),
		netnsHelperEnvVar+"=1",
		netnsBridgeDirEnvVar+"="+dir,
		netnsForwardPortEnvVar+"="+portStr,
	)

	return &netnsBridge{
		dir:         dir,
		listener:    listener,
		payloadPath: helperPath,
		payloadArgs: append([]string{netnsHelperArg, resolvedOrigPath}, origArgs...),
		env:         newEnv,
	}, nil
}

// extraBwrapArgs returns the additional bwrap flags needed to make the
// trampoline binary and the bridge dir itself visible inside the sandbox's
// mount namespace:
//
//   - the trampoline binary (--ro-bind): re-binding a path already covered
//     by an earlier --ro-bind (e.g. if Nanite happens to be installed
//     under /usr) is a harmless no-op layered mount, and most real
//     deployments (a custom build output path, or Cerberus's
//     ~/.cerberus/apps/... artifact path) are NOT already covered, so the
//     bind is required in practice.
//   - the bridge dir (--bind, read-write): netnsBridgeRoot ("/tmp") is
//     replaced inside the sandbox by the --tmpfs /tmp mount earlier in
//     applyOSSandbox's arg list, so without this explicit bind the
//     trampoline's own bind-mounted view of /tmp would be empty. bwrap
//     applies mounts in argument order, so this bind (appended after the
//     tmpfs mount) correctly overlays just this subpath, the same pattern
//     the sandboxDir/extraWritePath binds already rely on.
func (b *netnsBridge) extraBwrapArgs() []string {
	return []string{
		"--ro-bind", b.payloadPath, b.payloadPath,
		"--bind", b.dir, b.dir,
	}
}

// Close tears down the host-side listener and removes the bridge dir.
// Safe to call as the cleanup func applyOSSandbox returns to its caller.
func (b *netnsBridge) Close() {
	if b == nil {
		return
	}
	if b.listener != nil {
		_ = b.listener.Close()
	}
	if b.dir != "" {
		_ = os.RemoveAll(b.dir)
	}
}

func serveNetnsBridgeHostSide(listener net.Listener, proxyAddr string) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			if isNetnsBridgeClosedErr(err) {
				return
			}
			continue
		}
		safego.Go(context.Background(), "sandbox.netns-bridge.host-conn", func() {
			handleNetnsBridgeHostConn(conn, proxyAddr)
		})
	}
}

func handleNetnsBridgeHostConn(conn net.Conn, proxyAddr string) {
	defer conn.Close()
	target, err := (&net.Dialer{Timeout: 3 * time.Second}).Dial("tcp4", proxyAddr)
	if err != nil {
		return
	}
	defer target.Close()
	relayNetnsBridgeConns(conn, target)
}

// --- sandbox side: the re-exec trampoline -----------------------------

type ifreq struct {
	name [syscall.IFNAMSIZ]byte
	data [24]byte
}

// bringInterfaceUp brings the named interface up via a raw SIOCSIFFLAGS
// ioctl. No `ip`/`ifconfig` binary is bound into the narrow bwrap root, so
// this has to be done in-process. bwrap's --unshare-net leaves the
// namespace-local `lo` device present but administratively down — without
// this, nothing (not even same-namespace loopback traffic) would route.
func bringInterfaceUp(name string) error {
	sock, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(sock)

	var ifr ifreq
	copy(ifr.name[:], name)

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(sock), uintptr(syscall.SIOCGIFFLAGS), uintptr(unsafe.Pointer(&ifr)))
	if errno != 0 {
		return errno
	}

	flags := *(*uint16)(unsafe.Pointer(&ifr.data[0]))
	flags |= uint16(syscall.IFF_UP)
	*(*uint16)(unsafe.Pointer(&ifr.data[0])) = flags

	_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, uintptr(sock), uintptr(syscall.SIOCSIFFLAGS), uintptr(unsafe.Pointer(&ifr)))
	if errno != 0 {
		return errno
	}
	return nil
}

func fatalNetnsHelper(msg string) {
	fmt.Fprintln(os.Stderr, "nanite sandbox netns helper:", msg)
	os.Exit(125)
}

// filteredNetnsHelperEnv strips this file's own implementation-detail env
// vars before the trampoline execs the real wrapped command, so the
// agent's actual process sees exactly the restricted env AgentExec built
// for it — no leakage of bridge internals.
func filteredNetnsHelperEnv() []string {
	prefixes := []string{
		netnsHelperEnvVar + "=",
		netnsBridgeDirEnvVar + "=",
		netnsForwardPortEnvVar + "=",
	}
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		skip := false
		for _, p := range prefixes {
			if strings.HasPrefix(kv, p) {
				skip = true
				break
			}
		}
		if !skip {
			filtered = append(filtered, kv)
		}
	}
	return filtered
}

// runNetnsHelper is the first re-exec identity (see package doc comment,
// step 3): bring lo up, bind the sandbox-local loopback listener ITSELF
// (synchronously, guaranteed complete before anything else runs), hand
// that already-listening socket to the supervisor as an inherited fd, then
// exec the original wrapped command in place.
//
// Binding here rather than letting the supervisor bind independently after
// being spawned closes a real startup race: TCP accepts connections into
// the kernel backlog as soon as listen() returns, even before any process
// calls accept() on it — so as long as bind+listen happens before the real
// wrapped command starts making requests (guaranteed, since the exec below
// happens after), there is no window where HTTP_PROXY points at a port
// nothing is listening on yet, regardless of how long the supervisor
// process takes to actually start accepting.
func runNetnsHelper() int {
	if len(os.Args) < 3 || os.Args[1] != netnsHelperArg {
		fatalNetnsHelper("invalid helper invocation")
	}
	if err := bringInterfaceUp("lo"); err != nil {
		fatalNetnsHelper(fmt.Sprintf("bring up loopback: %v", err))
	}

	bridgeDir := os.Getenv(netnsBridgeDirEnvVar)
	portStr := os.Getenv(netnsForwardPortEnvVar)
	if bridgeDir == "" || portStr == "" {
		fatalNetnsHelper("missing netns bridge configuration")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		fatalNetnsHelper(fmt.Sprintf("invalid forward port %q: %v", portStr, err))
	}

	tcpListener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	if err != nil {
		fatalNetnsHelper(fmt.Sprintf("listen on sandbox loopback :%d: %v", port, err))
	}
	listenerFile, err := tcpListener.File() // dup()s the underlying fd
	if err != nil {
		fatalNetnsHelper(fmt.Sprintf("export loopback listener fd: %v", err))
	}
	// The dup in File() means closing our own copy doesn't affect the fd
	// the supervisor child inherits.
	_ = tcpListener.Close()

	if err := startNetnsSupervisorProcess(bridgeDir, listenerFile); err != nil {
		fatalNetnsHelper(fmt.Sprintf("start netns supervisor: %v", err))
	}
	_ = listenerFile.Close() // our copy; the child has its own dup via ExtraFiles

	targetPath := os.Args[2]
	targetArgs := append([]string{targetPath}, os.Args[3:]...)
	if err := syscall.Exec(targetPath, targetArgs, filteredNetnsHelperEnv()); err != nil {
		fatalNetnsHelper(fmt.Sprintf("exec target %q: %v", targetPath, err))
	}
	return 0 // unreachable; syscall.Exec only returns on error
}

// startNetnsSupervisorProcess spawns the supervisor identity as a genuine
// child process (fork+exec via os/exec — a bare fork() is unsafe in a
// multi-threaded Go runtime), inheriting the already-bound loopback
// listener as fd 3 via ExtraFiles. Pdeathsig ties its lifetime to the
// helper process so it cannot outlive the sandboxed command.
func startNetnsSupervisorProcess(bridgeDir string, listenerFile *os.File) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(exe); resolveErr == nil {
		exe = resolved
	}
	cmd := exec.Command(exe, netnsSupervisorArg)
	cmd.Env = append(filteredNetnsHelperEnv(),
		netnsHelperEnvVar+"=1",
		netnsBridgeDirEnvVar+"="+bridgeDir,
	)
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{listenerFile} // becomes fd 3 in the child
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGTERM,
		Setpgid:   true,
	}
	return cmd.Start()
}

// netnsSupervisorListenerFD is the fixed fd number the supervisor's
// inherited loopback listener arrives on: 0/1/2 are stdio, ExtraFiles
// starts at 3, and the supervisor is only ever given exactly one.
const netnsSupervisorListenerFD = 3

// runNetnsSupervisor is the second re-exec identity (see package doc
// comment, step 4): resume the loopback listener the helper already bound
// (inherited as fd 3), relaying every accepted connection to the
// bind-mounted Unix-domain bridge socket.
func runNetnsSupervisor() int {
	bridgeDir := os.Getenv(netnsBridgeDirEnvVar)
	if bridgeDir == "" {
		fatalNetnsHelper("missing netns supervisor configuration")
	}

	f := os.NewFile(uintptr(netnsSupervisorListenerFD), "netns-bridge-listener")
	if f == nil {
		fatalNetnsHelper("inherited loopback listener fd missing")
	}
	listener, err := net.FileListener(f)
	if err != nil {
		fatalNetnsHelper(fmt.Sprintf("resume inherited loopback listener: %v", err))
	}
	_ = f.Close() // FileListener dup()s; close our copy of the raw fd wrapper
	defer listener.Close()

	socketPath := filepath.Join(bridgeDir, netnsBridgeSocketName)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if isNetnsBridgeClosedErr(err) {
				return 0
			}
			continue
		}
		go handleNetnsBridgeSandboxConn(conn, socketPath)
	}
}

func handleNetnsBridgeSandboxConn(conn net.Conn, socketPath string) {
	defer conn.Close()
	target, err := (&net.Dialer{Timeout: 3 * time.Second}).Dial("unix", socketPath)
	if err != nil {
		return
	}
	defer target.Close()
	relayNetnsBridgeConns(conn, target)
}

// --- shared relay ------------------------------------------------------

func relayNetnsBridgeConns(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		if tcp, ok := a.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		} else {
			_ = a.Close()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		if tcp, ok := b.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		} else {
			_ = b.Close()
		}
	}()
	wg.Wait()
}

func isNetnsBridgeClosedErr(err error) bool {
	return errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection")
}
