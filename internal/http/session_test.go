package http

import (
	"testing"
	"time"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func TestSession_RoundTrip(t *testing.T) {
	s := session{
		Username:  "F920U2K",
		Email:     "ben@example.com",
		Expires:   time.Now().Add(time.Hour).UTC().Round(time.Second),
		CSRFToken: "csrf-abc-123",
	}
	enc, err := encodeSession(testKey, s)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeSession(testKey, enc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != s.Username ||
		got.Email != s.Email || got.CSRFToken != s.CSRFToken {
		t.Errorf("round-trip mismatch: %+v vs %+v", got, s)
	}
}

func TestSession_RejectsTampering(t *testing.T) {
	enc, _ := encodeSession(testKey, session{Username: "alice", Expires: time.Now().Add(time.Hour)})
	tampered := enc[:len(enc)-2] + "xx"
	if _, err := decodeSession(testKey, tampered); err == nil {
		t.Error("expected signature error")
	}
}

func TestSession_RejectsWrongKey(t *testing.T) {
	enc, _ := encodeSession(testKey, session{Username: "alice", Expires: time.Now().Add(time.Hour)})
	other := []byte("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	if _, err := decodeSession(other, enc); err == nil {
		t.Error("expected signature error with wrong key")
	}
}

func TestSession_RejectsExpired(t *testing.T) {
	enc, _ := encodeSession(testKey, session{Username: "alice", Expires: time.Now().Add(-time.Hour)})
	if _, err := decodeSession(testKey, enc); err == nil {
		t.Error("expected expired error")
	}
}
