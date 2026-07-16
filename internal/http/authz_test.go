package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/cf-mgmt-portal/internal/cfapi"
)

// newFakeCFAPI stands up one httptest server acting as both UAA (token + SCIM
// Users) and CF API (v3 org/user/role lookups), and returns a real
// cfapi.Client pointed at it. usersHandler serves GET /Users.
func newFakeCFAPI(t *testing.T, usersHandler http.HandlerFunc, hasOrgRole bool) *cfapi.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"t","token_type":"bearer","expires_in":3600}`)
	})
	mux.HandleFunc("/Users", usersHandler)
	mux.HandleFunc("/v3/organizations", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"resources":[{"guid":"org-guid"}]}`)
	})
	mux.HandleFunc("/v3/users", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"resources":[{"guid":"user-guid"}]}`)
	})
	mux.HandleFunc("/v3/roles", func(w http.ResponseWriter, _ *http.Request) {
		if hasOrgRole {
			_, _ = io.WriteString(w, `{"resources":[{}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"resources":[]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return cfapi.NewClient(srv.URL, srv.URL, "id", "secret")
}

func TestVerifyOrgManager_AdminGroupSkipsRoleCheck(t *testing.T) {
	c := newFakeCFAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"resources":[{"groups":[{"display":"platform.admins"}]}]}`)
	}, false) // hasOrgRole=false: only the group membership can let this pass
	s := &Server{deps: Deps{CFAPI: c, AdminGroups: []string{"platform.admins"}}}
	rec := NewRecorder()

	if err := s.verifyOrgManager(context.Background(), rec, session{Username: "F920U2K"}, "system"); err != nil {
		t.Fatalf("expected group bypass, got error: %v", err)
	}
	last := rec.Steps[len(rec.Steps)-1]
	if last.Status != "skip" {
		t.Fatalf("expected final step skipped, got %+v", rec.Steps)
	}
}

func TestVerifyOrgManager_NonMemberFallsThroughToRoleCheck(t *testing.T) {
	c := newFakeCFAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"resources":[{"groups":[{"display":"uaa.user"}]}]}`)
	}, true)
	s := &Server{deps: Deps{CFAPI: c, AdminGroups: []string{"platform.admins"}}}
	rec := NewRecorder()

	if err := s.verifyOrgManager(context.Background(), rec, session{Username: "F920U2K"}, "system"); err != nil {
		t.Fatalf("expected role check to pass, got error: %v", err)
	}
	last := rec.Steps[len(rec.Steps)-1]
	if last.Status != "ok" {
		t.Fatalf("expected final step ok (role check ran), got %+v", rec.Steps)
	}
}

func TestVerifyOrgManager_GroupLookupFailureFallsThroughToRoleCheck(t *testing.T) {
	c := newFakeCFAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden) // e.g. service account lacks scim.read
	}, true)
	s := &Server{deps: Deps{CFAPI: c, AdminGroups: []string{"platform.admins"}}}
	rec := NewRecorder()

	if err := s.verifyOrgManager(context.Background(), rec, session{Username: "F920U2K"}, "system"); err != nil {
		t.Fatalf("expected fallback to role check, got error: %v", err)
	}
	if rec.Steps[0].Status != "error" {
		t.Fatalf("expected group step recorded as error, got %+v", rec.Steps)
	}
	last := rec.Steps[len(rec.Steps)-1]
	if last.Status != "ok" {
		t.Fatalf("expected final step ok (role check ran), got %+v", rec.Steps)
	}
}

func TestVerifyOrgManager_NonMemberNonManagerRejected(t *testing.T) {
	c := newFakeCFAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"resources":[{"groups":[{"display":"uaa.user"}]}]}`)
	}, false)
	s := &Server{deps: Deps{CFAPI: c, AdminGroups: []string{"platform.admins"}}}
	rec := NewRecorder()

	if err := s.verifyOrgManager(context.Background(), rec, session{Username: "F920U2K"}, "system"); err == nil {
		t.Fatal("expected rejection for non-member non-manager")
	}
}

func TestVerifyOrgManager_PortalAdminSkipsCFCheck(t *testing.T) {
	// CFAPI is nil: if the admin path ever consulted CF, this would panic.
	s := &Server{deps: Deps{AdminUsers: []string{"admin", "F920U2K"}}}
	rec := NewRecorder()

	if err := s.verifyOrgManager(context.Background(), rec, session{Username: "admin"}, "system"); err != nil {
		t.Fatalf("expected admin bypass, got error: %v", err)
	}
	if len(rec.Steps) != 1 || rec.Steps[0].Status != "skip" {
		t.Fatalf("expected a single skipped step, got %+v", rec.Steps)
	}
}

func TestIsPortalAdmin(t *testing.T) {
	s := &Server{deps: Deps{AdminUsers: []string{"admin"}}}
	for user, want := range map[string]bool{
		"admin": true,
		"Admin": false, // exact match only
		"jdoe":  false,
		"":      false,
	} {
		if got := s.isPortalAdmin(user); got != want {
			t.Errorf("isPortalAdmin(%q) = %v, want %v", user, got, want)
		}
	}
}
