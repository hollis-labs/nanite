package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	pluginhost "github.com/hollis-labs/plugin-host"
)

var ErrSubprocessGone = pluginhost.ErrGone

const MaxCallDuration = 30 * time.Second

// Transport adapts Nanite's RPC consumers to the shared connection. A supervised
// transport resolves the current child on every call, including after restart.
type Transport struct {
	conn      *pluginhost.Conn
	current   func() *pluginhost.Conn
	secrets   []string
	callLimit time.Duration
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
	conn, err := t.connection()
	if err != nil {
		return nil, err
	}
	limit := t.callLimit
	if limit <= 0 {
		limit = MaxCallDuration
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	result, err := conn.Call(ctx, method, params)
	if err != nil {
		return nil, redactPluginError(err, t.secrets)
	}
	return &RPCResponse{JSONRPC: "2.0", Result: result}, nil
}

func (t *Transport) Notify(method string, params any) error {
	conn, err := t.connection()
	if err != nil {
		return err
	}
	return conn.Notify(method, params)
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
