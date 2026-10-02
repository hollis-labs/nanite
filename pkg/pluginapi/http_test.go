package pluginapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	sdkprocess "github.com/hollis-labs/plugin-sdk/subprocess"
)

func TestHandleHTTPPreservesSubtreePathAndQuery(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /bookmarks/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "one/two" || r.URL.RawPath != "/bookmarks/one%2Ftwo" || r.URL.RawQuery != "a=1&a=2&empty=&bare" || len(r.URL.Query()["a"]) != 2 {
			t.Errorf("lost URL fields: %+v id=%q", r.URL, r.PathValue("id"))
		}
		if r.Header.Get("X-Test") != "value" {
			t.Error("lost header")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(r.URL.Query())
	})
	response, err := HandleHTTP(context.Background(), "bookmarks", mux, sdkprocess.HTTPRequest{Method: "GET", Path: "/api/plugins/bookmarks/bookmarks/one/two", RawPath: "/api/plugins/bookmarks/bookmarks/one%2Ftwo", RawQuery: "a=1&a=2&empty=&bare", Headers: map[string]string{"X-Test": "value"}})
	if err != nil || response.Status != 202 || !strings.Contains(string(response.Body), `"a":["1","2"]`) {
		t.Fatalf("response=%+v error=%v", response, err)
	}
}
func TestHandleHTTPRefusesMalformedOrOversizedTraffic(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	base := sdkprocess.HTTPRequest{Method: "POST", Path: "/api/plugins/bookmarks/bookmarks"}
	for _, change := range []func(*sdkprocess.HTTPRequest){
		func(r *sdkprocess.HTTPRequest) { r.Path = "/api/plugins/other/bookmarks" },
		func(r *sdkprocess.HTTPRequest) { r.Path = ContextFetchPath },
		func(r *sdkprocess.HTTPRequest) { r.RawPath = "/api/plugins/bookmarks/other" },
		func(r *sdkprocess.HTTPRequest) { r.RawQuery = "a=%xx" },
		func(r *sdkprocess.HTTPRequest) { r.Headers = map[string]string{"Bad\nHeader": "value"} },
		func(r *sdkprocess.HTTPRequest) { r.Body = make([]byte, MaxHTTPBody+1) },
	} {
		call := base
		change(&call)
		if _, err := HandleHTTP(context.Background(), "bookmarks", handler, call); err == nil {
			t.Errorf("accepted malformed call %+v", call)
		}
	}
	for _, bad := range []http.Handler{
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, MaxHTTPBody+1)) }),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(700) }),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("X-Test", "bad\r\nvalue") }),
	} {
		if _, err := HandleHTTP(context.Background(), "bookmarks", bad, base); err == nil {
			t.Error("accepted invalid response")
		}
	}
}
