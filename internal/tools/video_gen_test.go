package tools_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type videoAPIProbe struct {
	mu        sync.Mutex
	posts     int
	polls     int
	downloads int
	lastAuth  string
	lastBody  map[string]any
	inlineB64 string
}

func startVideoAPI(t *testing.T, mp4 []byte) (*httptest.Server, *videoAPIProbe) {
	t.Helper()
	p := &videoAPIProbe{}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/videos/generations":
			p.posts++
			p.lastAuth = r.Header.Get("Authorization")
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &p.lastBody)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"request_id":"req-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/videos/req-1":
			p.polls++
			if p.polls == 1 {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			if p.inlineB64 != "" {
				_, _ = fmt.Fprintf(w, `{"status":"done","video":{"b64_json":%q}}`, p.inlineB64)
				return
			}
			_, _ = fmt.Fprintf(w, `{"status":"done","video":{"url":%q}}`, srv.URL+"/clip.mp4")
		case r.URL.Path == "/clip.mp4":
			p.downloads++
			_, _ = w.Write(mp4)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, p
}

func (p *videoAPIProbe) snapshot() (posts, polls, downloads int, auth string, body map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.posts, p.polls, p.downloads, p.lastAuth, p.lastBody
}

func isolation(t *testing.T) {
	t.Helper()
	t.Setenv("ENTROPY_HOME", t.TempDir())
}
