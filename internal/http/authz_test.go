package http

import (
	"context"
	"testing"
)

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
