package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	pluginhost "github.com/hollis-labs/libs/plugin-mcp/plugin-host"
)

var ErrSubprocessGone = pluginhost.ErrGone

const MaxCallDuration = 30 * time.Second

// Transport adapts Nanite's RPC consumers to the shared connection. A supervised
// transport resolves the current child on every call, including after restart.
type Transport struct {
	conn      *pluginhost.Conn
	current   func() *pluginhost.Conn
	secrets   []string
	secretMu  sync.RWMutex
	callLimit time.Duration
	dispatch  func(context.Context) (*pluginhost.Conn, context.Context, func(), error)
}

func NewTransport(r io.Reader, w io.Writer) *Transport {
	return &Transport{conn: pluginhost.NewConn(r, w)}
}

func (t *Transport) connection() (*pluginhost.Conn, error) {
	if t == nil {
		return nil, ErrSubprocessGone
	}
	conn := t.conn
	if t.current != nil {
		conn = t.current()
	}
	if conn == nil {
		return nil, ErrSubprocessGone
	}
	return conn, nil
}

func (t *Transport) Close() error {
	if t == nil {
		return nil
	}
	conn := t.conn
	if t.current != nil {
		conn = t.current()
	}
	if conn == nil {
		return nil
	}
	return conn.Close()
}

func (t *Transport) Call(ctx context.Context, method string, params any) (*RPCResponse, error) {
	conn, permitted, release, err := t.acquireDispatch(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx = permitted
	limit := t.callLimit
	if limit <= 0 {
		limit = MaxCallDuration
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	result, err := conn.Call(ctx, method, params)
	if err != nil {
		return nil, redactPluginError(err, t.secretValues())
	}
	return &RPCResponse{JSONRPC: "2.0", Result: result}, nil
}

func (t *Transport) Notify(method string, params any) error {
	conn, ctx, release, err := t.acquireDispatch(context.Background())
	if err != nil {
		return err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return err
	}
	return conn.Notify(method, params)
}

func (t *Transport) acquireDispatch(ctx context.Context) (*pluginhost.Conn, context.Context, func(), error) {
	if t != nil && t.dispatch != nil {
		return t.dispatch(ctx)
	}
	conn, err := t.connection()
	return conn, ctx, func() {}, err
}

func CallResult[T any](t *Transport, ctx context.Context, method string, params any) (*T, error) {
	resp, err := t.Call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	var result T
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal %s result: %w", method, err)
	}
	return &result, nil
}

type redactedPluginError struct {
	cause   error
	message string
}

func (err redactedPluginError) Error() string { return err.message }
func (err redactedPluginError) Unwrap() error { return err.cause }
func redactPluginError(err error, values []string) error {
	if err == nil || len(values) == 0 {
		return err
	}
	message := pluginhost.Redact(err.Error(), values)
	if message == err.Error() {
		return err
	}
	return redactedPluginError{cause: err, message: message}
}

func (t *Transport) addSecrets(values []string) {
	if t == nil {
		return
	}
	t.secretMu.Lock()
	defer t.secretMu.Unlock()
	t.secrets = append(t.secrets, values...)
}
func (t *Transport) secretValues() []string {
	t.secretMu.RLock()
	defer t.secretMu.RUnlock()
	return append([]string(nil), t.secrets...)
}
