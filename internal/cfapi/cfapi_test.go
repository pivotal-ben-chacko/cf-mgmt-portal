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
	return &Client{apiURL: srv.URL, uaaURL: srv.URL, http: srv.Client()}
}

func TestUserGroups_UnionsAcrossOriginsAndDedupes(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Users" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			return
		}
		q := r.URL.Query()
		if got := q.Get("filter"); got != `userName eq "F920U2K"` {
			t.Errorf("filter query: %q", got)
		}
		if got := q.Get("attributes"); got != "userName,groups" {
			t.Errorf("attributes query: %q", got)
		}
		// Two identities for the same username (uaa + ldap origins) with an
		// overlapping group.
		_, _ = io.WriteString(w, `{"resources":[
			{"groups":[{"display":"uaa.user"},{"display":"platform.admins"}]},
			{"groups":[{"display":"platform.admins"},{"display":"cloud_controller.admin"}]}
		]}`)
	})
	got, err := c.UserGroups(context.Background(), "F920U2K")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"uaa.user", "platform.admins", "cloud_controller.admin"}
	if len(got) != len(want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("groups = %v, want %v", got, want)
		}
	}
}

func TestUserGroups_UnknownUserReturnsEmpty(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"resources":[]}`)
	})
	got, err := c.UserGroups(context.Background(), "F00NULL")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected no groups, got %v", got)
	}
}

func TestUserGroups_PropagatesAPIError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"insufficient_scope"}`)
	})
	if _, err := c.UserGroups(context.Background(), "F920U2K"); err == nil {
		t.Fatal("expected error when scim.read is missing")
	}
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
