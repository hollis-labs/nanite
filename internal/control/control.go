// Package control exposes the local owner's stop-all operation over a private
// Unix socket. A temporary directory keeps socket paths below Unix length limits.
package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const endpointFile = "stop.endpoint"

type Server struct {
	server   *http.Server
	dir      string
	endpoint string
}

// Start requires the caller to hold the agent data directory's exclusive lock.
func Start(data string, stop func(context.Context) error) (*Server, error) {
	dir, err := os.MkdirTemp("", "nanite-control-")
	if err != nil {
		return nil, err
	}
	socket := filepath.Join(dir, "stop.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	s := &Server{dir: dir, endpoint: filepath.Join(data, endpointFile)}
	s.server = &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/stop-all" {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := stop(ctx); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})}
	file, err := os.CreateTemp(data, ".stop-endpoint-")
	if err == nil {
		_, err = io.WriteString(file, socket)
		err = errors.Join(err, file.Close())
		if err == nil {
			err = os.Rename(file.Name(), s.endpoint)
		}
		_ = os.Remove(file.Name())
	}
	if err != nil {
		_ = listener.Close()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	go func() { _ = s.server.Serve(listener) }()
	return s, nil
}

func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	err := s.server.Close()
	_ = os.Remove(s.endpoint)
	return errors.Join(err, os.RemoveAll(s.dir))
}

// Stop asks the process owning data to stop all agent work.
func Stop(ctx context.Context, data string) error {
	raw, err := os.ReadFile(filepath.Join(data, endpointFile)) // #nosec G304 -- The owner selects the private agent state directory.
	if err != nil {
		return fmt.Errorf("agent control endpoint: %w", err)
	}
	socket := strings.TrimSpace(string(raw))
	if socket == "" || len(socket) > 4096 {
		return errors.New("invalid agent control endpoint")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 35 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://local/stop-all", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("stop agent: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("stop agent: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
