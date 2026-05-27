package http

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestVerifyCSRF_MatchesToken(t *testing.T) {
	sess := session{CSRFToken: "abc123"}
	form := url.Values{"csrf_token": []string{"abc123"}}
	r := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = r.ParseForm()
	if !verifyCSRF(r, sess) {
		t.Error("expected match")
	}
}

func TestVerifyCSRF_RejectsMismatch(t *testing.T) {
	sess := session{CSRFToken: "abc123"}
	form := url.Values{"csrf_token": []string{"xyz789"}}
	r := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = r.ParseForm()
	if verifyCSRF(r, sess) {
		t.Error("expected mismatch to fail")
	}
}

func TestVerifyCSRF_RejectsEmptyFormToken(t *testing.T) {
	sess := session{CSRFToken: "abc123"}
	r := httptest.NewRequest("POST", "/", strings.NewReader(""))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = r.ParseForm()
	if verifyCSRF(r, sess) {
		t.Error("expected empty form token to fail")
	}
}

func TestVerifyCSRF_RejectsEmptySessionToken(t *testing.T) {
	sess := session{CSRFToken: ""}
	form := url.Values{"csrf_token": []string{"abc123"}}
	r := httptest.NewRequest("POST", "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = r.ParseForm()
	if verifyCSRF(r, sess) {
		t.Error("expected empty session token to fail (defends against pre-CSRF sessions)")
	}
}
