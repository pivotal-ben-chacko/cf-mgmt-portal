package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthURL_IncludesStateAndScope(t *testing.T) {
	c, err := NewUAAOAuth("https://uaa.example/", "cid", "csecret", "https://portal/cb")
	if err != nil {
		t.Fatal(err)
	}
	u := c.AuthURL("xyz123")
	if !strings.Contains(u, "https://uaa.example/oauth/authorize") {
		t.Errorf("wrong host: %s", u)
	}
	for _, want := range []string{"state=xyz123", "scope=openid", "client_id=cid", "response_type=code"} {
		if !strings.Contains(u, want) {
			t.Errorf("missing %q in %s", want, u)
		}
	}
}

func TestExchange_ReturnsUserFromUserinfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok-abc", "token_type": "bearer", "expires_in": 3600,
			})
		case "/userinfo":
			if got := r.Header.Get("Authorization"); got != "Bearer tok-abc" {
				t.Errorf("userinfo auth header = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"user_id":   "8a3f-...",
				"user_name": "F920U2K",
				"email":     "jane.doe@example.com",
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := NewUAAOAuth(srv.URL, "cid", "csecret", "https://portal/cb")
	if err != nil {
		t.Fatal(err)
	}
	u, err := c.Exchange(context.Background(), "some-code")
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "F920U2K" || u.Email != "jane.doe@example.com" {
		t.Errorf("got %+v", u)
	}
}

func TestExchange_RejectsEmptyUsername(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth/token":
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok-abc", "token_type": "bearer", "expires_in": 3600,
			})
		default:
			json.NewEncoder(w).Encode(map[string]any{"email": "x@example.com"})
		}
	}))
	defer srv.Close()

	c, err := NewUAAOAuth(srv.URL, "cid", "csecret", "https://portal/cb")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exchange(context.Background(), "some-code"); err == nil {
		t.Fatal("expected error for empty user_name, got nil")
	}
}
