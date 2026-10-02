package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	pluginhost "github.com/hollis-labs/plugin-host"
)

var ErrSubprocessGone = pluginhost.ErrGone

// Transport adapts Nanite's RPC consumers to the shared connection. A supervised
// transport resolves the current child on every call, including after restart.
type Transport struct {
	conn    *pluginhost.Conn
	current func() *pluginhost.Conn
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
	result, err := conn.Call(ctx, method, params)
	if err != nil {
		return nil, err
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
