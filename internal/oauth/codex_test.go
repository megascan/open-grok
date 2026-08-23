package oauth_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/megascan/open-grok/internal/oauth"
)

func TestExchangeAndRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form := string(body)
		switch {
		case strings.Contains(r.URL.Path, "/oauth/token") && strings.Contains(form, "authorization_code"):
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id_token": "id", "access_token": "acc", "refresh_token": "ref",
			})
		case strings.Contains(r.URL.Path, "/oauth/token") && strings.Contains(form, "refresh_token"):
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token": "acc2", "refresh_token": "ref2",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ep := oauth.Endpoints{Issuer: srv.URL, ClientID: "app"}
	tok, err := oauth.ExchangeCode(context.Background(), ep, "code", "http://localhost/cb", "ver")
	if err != nil || tok.AccessToken != "acc" {
		t.Fatalf("exchange %v %+v", err, tok)
	}
	tok, err = oauth.Refresh(context.Background(), ep, "ref")
	if err != nil || tok.AccessToken != "acc2" {
		t.Fatalf("refresh %v %+v", err, tok)
	}
}

func TestStartBrowserLoginCallbackExchangesCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/oauth/token") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id_token": "id", "access_token": "acc", "refresh_token": "ref",
		})
	}))
	defer srv.Close()

	sess, err := oauth.StartBrowserLogin(oauth.Endpoints{Issuer: srv.URL, ClientID: "app"})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	u, err := url.Parse(sess.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatal("authorize URL omitted state")
	}
	resp, err := http.Get(sess.RedirectURL + "?code=test-code&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("callback %s", resp.Status)
	}
	tok, err := sess.Wait(context.Background())
	if err != nil || tok.AccessToken != "acc" {
		t.Fatalf("wait %v %+v", err, tok)
	}
}

func TestStartBrowserLoginCancel(t *testing.T) {
	sess, err := oauth.StartBrowserLogin(oauth.Endpoints{Issuer: "http://127.0.0.1:1", ClientID: "app"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = sess.Wait(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
