package cfapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient bypasses the oauth2 constructor wrap so we can assert against
// raw HTTP requests without standing up a fake UAA token endpoint.
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{apiURL: srv.URL, http: srv.Client()}
}

func TestUserHasOrgRole_TrueWhenAllLookupsSucceed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/organizations":
			if got := r.URL.Query().Get("names"); got != "epay-org" {
				t.Errorf("org names query: %q", got)
			}
			_, _ = io.WriteString(w, `{"resources":[{"guid":"org-guid"}]}`)
		case "/v3/users":
			q := r.URL.Query()
			if q.Get("usernames") != "F920U2K" || q.Get("origins") != "ldap" {
				t.Errorf("users query: %v", q)
			}
			_, _ = io.WriteString(w, `{"resources":[{"guid":"user-guid"}]}`)
		case "/v3/roles":
			q := r.URL.Query()
			if q.Get("organization_guids") != "org-guid" ||
				q.Get("user_guids") != "user-guid" ||
				q.Get("types") != "organization_manager" {
				t.Errorf("roles query: %v", q)
			}
			_, _ = io.WriteString(w, `{"resources":[{}]}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
	})
	ok, err := c.UserHasOrgRole(context.Background(), "F920U2K", "epay-org", RoleOrgManager)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("expected true")
	}
}

func TestUserHasOrgRole_FalseWhenUserNotInCF(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/organizations":
			_, _ = io.WriteString(w, `{"resources":[{"guid":"org-guid"}]}`)
		case "/v3/users":
			_, _ = io.WriteString(w, `{"resources":[]}`)
		default:
			t.Errorf("should not reach %s", r.URL.Path)
		}
	})
	ok, err := c.UserHasOrgRole(context.Background(), "F00NULL", "epay-org", RoleOrgManager)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected false for unknown user")
	}
}

func TestUserHasOrgRole_ErrorsWhenOrgMissing(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"resources":[]}`)
	})
	_, err := c.UserHasOrgRole(context.Background(), "F920U2K", "nope-org", RoleOrgManager)
	if err == nil {
		t.Fatal("expected error for missing org")
	}
}

func TestUserHasOrgRole_FalseWhenRoleAbsent(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/organizations":
			_, _ = io.WriteString(w, `{"resources":[{"guid":"org-guid"}]}`)
		case "/v3/users":
			_, _ = io.WriteString(w, `{"resources":[{"guid":"user-guid"}]}`)
		case "/v3/roles":
			_, _ = io.WriteString(w, `{"resources":[]}`)
		}
	})
	ok, err := c.UserHasOrgRole(context.Background(), "F920U2K", "epay-org", RoleOrgManager)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected false when user has no role")
	}
}

func TestUserHasOrgRole_PropagatesAPIError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"errors":[{"detail":"oops"}]}`)
	})
	_, err := c.UserHasOrgRole(context.Background(), "F920U2K", "epay-org", RoleOrgManager)
	if err == nil {
		t.Fatal("expected error")
	}
}
