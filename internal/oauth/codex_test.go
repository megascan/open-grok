package oauth_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
