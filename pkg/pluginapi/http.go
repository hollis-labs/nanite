package pluginapi

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	sdkprocess "github.com/hollis-labs/plugin-sdk/subprocess"
)

// MaxHTTPBody bounds buffered requests and responses. Streaming remains a
// host-owned SSE path rather than a subprocess http/handle operation.
const MaxHTTPBody = 4 << 20
const maxHTTPHeaders = 64 << 10

// HandleHTTP adapts an SDK request to a plugin-local net/http handler. A route
// registered as "bookmarks/" reaches the handler as "/bookmarks/<id>".
// Dispatch private operations such as ContextFetchPath before calling this
// adapter. The supplied context retains cancellation; SessionID and Identity
// stay on the SDK request for the plugin to consume explicitly.
func HandleHTTP(ctx context.Context, pluginID string, handler http.Handler, call sdkprocess.HTTPRequest) (sdkprocess.HTTPResponse, error) {
	if !slug.MatchString(pluginID) || len(pluginID) > 64 || handler == nil || len(call.Body) > MaxHTTPBody {
		return sdkprocess.HTTPResponse{}, fmt.Errorf("pluginapi: invalid HTTP adapter input")
	}
	prefix := "/api/plugins/" + pluginID
	if !strings.HasPrefix(call.Path, prefix+"/") || strings.ContainsAny(call.Path, "\x00\r\n?#") {
		return sdkprocess.HTTPResponse{}, fmt.Errorf("pluginapi: HTTP path outside plugin namespace")
	}
	localPath := strings.TrimPrefix(call.Path, prefix)
	localRaw := ""
	if call.RawPath != "" {
		decoded, checkErr := url.PathUnescape(call.RawPath)
		if checkErr != nil || decoded != call.Path || !strings.HasPrefix(call.RawPath, prefix+"/") {
			return sdkprocess.HTTPResponse{}, fmt.Errorf("pluginapi: invalid raw HTTP path")
		}
		localRaw = strings.TrimPrefix(call.RawPath, prefix)
	}
	if _, checkErr := url.ParseQuery(call.RawQuery); checkErr != nil {
		return sdkprocess.HTTPResponse{}, fmt.Errorf("pluginapi: invalid raw HTTP query")
	}
	endpoint := &url.URL{Path: localPath, RawPath: localRaw, RawQuery: call.RawQuery}
	request, err := http.NewRequestWithContext(ctx, call.Method, endpoint.String(), bytes.NewReader(call.Body))
	if err != nil {
		return sdkprocess.HTTPResponse{}, err
	}
	request.RequestURI = endpoint.RequestURI()
	headerBytes := 0
	for key, value := range call.Headers {
		headerBytes += len(key) + len(value)
		if headerBytes > maxHTTPHeaders || !validHTTPHeader(key, value) {
			return sdkprocess.HTTPResponse{}, fmt.Errorf("pluginapi: invalid HTTP headers")
		}
		request.Header.Set(key, value)
	}
	writer := &httpBuffer{header: make(http.Header)}
	handler.ServeHTTP(writer, request)
	if writer.err != nil {
		return sdkprocess.HTTPResponse{}, writer.err
	}
	status := writer.status
	if status == 0 {
		status = http.StatusOK
	}
	headers := make(map[string]string, len(writer.header))
	headerBytes = 0
	for key, values := range writer.header {
		value := strings.Join(values, ", ")
		headerBytes += len(key) + len(value)
		if headerBytes > maxHTTPHeaders || !validHTTPHeader(key, value) {
			return sdkprocess.HTTPResponse{}, fmt.Errorf("pluginapi: invalid response headers")
		}
		headers[key] = value
	}
	return sdkprocess.HTTPResponse{Status: status, Headers: headers, Body: writer.body.Bytes()}, nil
}

func validHTTPHeader(key, value string) bool {
	if key == "" || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}

type httpBuffer struct {
	header http.Header
	status int
	body   bytes.Buffer
	err    error
}

func (w *httpBuffer) Header() http.Header { return w.header }
func (w *httpBuffer) WriteHeader(status int) {
	if w.status != 0 || w.err != nil {
		return
	}
	if status < 200 || status > 599 {
		w.err = fmt.Errorf("pluginapi: response status outside 200..599")
		return
	}
	w.status = status
}
func (w *httpBuffer) Write(data []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.body.Len()+len(data) > MaxHTTPBody {
		w.err = fmt.Errorf("pluginapi: HTTP response exceeds buffer limit")
		return 0, w.err
	}
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.body.Write(data)
}
