package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Sessions are stateless: a base64(payload).base64(hmac) cookie. Restarting
// the app does not log users out, and there is no server-side store.

const (
	sessionCookieName = "cf_mgmt_portal_session"
	sessionTTL        = 8 * time.Hour
)

type session struct {
	GitLabID  int       `json:"gid"`
	Username  string    `json:"u"`
	Email     string    `json:"e"`
	Expires   time.Time `json:"exp"`
	CSRFToken string    `json:"csrf"`
}

func encodeSession(key []byte, s session) (string, error) {
	payload, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(enc))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return enc + "." + sig, nil
}

func decodeSession(key []byte, value string) (session, error) {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) != 2 {
		return session{}, errors.New("malformed session")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(parts[0]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return session{}, errors.New("bad session signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return session{}, err
	}
	var s session
	if err := json.Unmarshal(payload, &s); err != nil {
		return session{}, err
	}
	if time.Now().After(s.Expires) {
		return session{}, errors.New("session expired")
	}
	return s, nil
}

func setSessionCookie(w http.ResponseWriter, key []byte, s session) error {
	value, err := encodeSession(key, s)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		Expires:  s.Expires,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func readSession(r *http.Request, key []byte) (session, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return session{}, err
	}
	return decodeSession(key, cookie.Value)
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   true,
	})
}
