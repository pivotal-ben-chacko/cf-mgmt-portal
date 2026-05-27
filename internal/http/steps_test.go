package http

import (
	"errors"
	"testing"
	"time"
)

func TestRecorder_Do_RecordsSuccess(t *testing.T) {
	rec := NewRecorder()
	err := rec.Do("step 1", func() (string, error) {
		return "fine", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Steps) != 1 || rec.Steps[0].Status != "ok" || rec.Steps[0].Detail != "fine" {
		t.Errorf("unexpected step: %+v", rec.Steps)
	}
}

func TestRecorder_Do_RecordsErrorAndReturnsIt(t *testing.T) {
	rec := NewRecorder()
	bang := errors.New("kaboom")
	err := rec.Do("step 1", func() (string, error) {
		return "", bang
	})
	if !errors.Is(err, bang) {
		t.Errorf("Do should return fn error: %v", err)
	}
	if len(rec.Steps) != 1 || rec.Steps[0].Status != "error" || rec.Steps[0].Detail != "kaboom" {
		t.Errorf("unexpected step: %+v", rec.Steps)
	}
}

func TestRecorder_Skip(t *testing.T) {
	rec := NewRecorder()
	rec.Skip("step 1", "not needed")
	if len(rec.Steps) != 1 || rec.Steps[0].Status != "skip" {
		t.Errorf("expected skip step, got %+v", rec.Steps)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{500 * time.Microsecond, "<1ms"},
		{12 * time.Millisecond, "12ms"},
		{999 * time.Millisecond, "999ms"},
		{1500 * time.Millisecond, "1.50s"},
		{2*time.Second + 500*time.Millisecond, "2.50s"},
	}
	for _, c := range cases {
		if got := formatDuration(c.in); got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
