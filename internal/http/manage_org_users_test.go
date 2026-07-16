package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestServerAndCookie builds a Server through NewServer (real routing) with
// only a session key wired, plus a valid signed session cookie for requests.
func newTestServerAndCookie(t *testing.T) (http.Handler, *http.Cookie) {
	t.Helper()
	key := []byte("0123456789abcdef0123456789abcdef")
	h := NewServer(Deps{SessionKey: key, Foundation: "fog"})
	value, err := encodeSession(key, session{
		Username:  "F920U2K",
		Expires:   time.Now().Add(time.Hour),
		CSRFToken: "tok",
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, &http.Cookie{Name: sessionCookieName, Value: value}
}

func TestManageOrgUsers_GetRendersForm(t *testing.T) {
	h, cookie := newTestServerAndCookie(t)
	r := httptest.NewRequest("GET", "/actions/manage-org-users", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"Manage org users", "org-billingmanager", "/api/org-users", `name="csrf_token"`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected page to contain %q", want)
		}
	}
}

func TestManageOrgUsers_GetRedirectsWithoutSession(t *testing.T) {
	h, _ := newTestServerAndCookie(t)
	r := httptest.NewRequest("GET", "/actions/manage-org-users", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/auth/login" {
		t.Fatalf("expected redirect to /auth/login, got %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestManageOrgUsers_PostRejectsBadCSRF(t *testing.T) {
	h, cookie := newTestServerAndCookie(t)
	form := "csrf_token=wrong&org=epay-org&ops=%5B%5D"
	r := httptest.NewRequest("POST", "/actions/manage-org-users", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for bad CSRF token, got %d", w.Code)
	}
}

func TestManageOrgUsers_PostRejectsEmptyOps(t *testing.T) {
	h, cookie := newTestServerAndCookie(t)
	form := "csrf_token=tok&org=epay-org&ops=%5B%5D"
	r := httptest.NewRequest("POST", "/actions/manage-org-users", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty ops, got %d", w.Code)
	}
}
