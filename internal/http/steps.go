package http

import (
	"fmt"
	"log"
	"time"
)

// Step is a single recorded action in a handler's run log.
type Step struct {
	Title    string
	Status   string // "ok" | "error" | "skip"
	Detail   string
	Duration time.Duration
}

// DurationLabel renders Duration as a compact human-friendly string for the
// result template.
func (s Step) DurationLabel() string {
	return formatDuration(s.Duration)
}

// Recorder collects timing and outcomes for each step of a handler so the
// result page can show a terminal-style log. Not safe for concurrent use; one
// Recorder per request.
type Recorder struct {
	Steps []Step
	Start time.Time
}

func NewRecorder() *Recorder {
	return &Recorder{Start: time.Now()}
}

// Do runs fn, records its outcome with timing, and returns fn's error verbatim.
// The detail string is shown next to the step on success; on error, if fn
// returned an empty detail, the error message becomes the detail.
func (r *Recorder) Do(title string, fn func() (detail string, err error)) error {
	start := time.Now()
	detail, err := fn()
	s := Step{Title: title, Duration: time.Since(start), Detail: detail}
	if err != nil {
		s.Status = "error"
		if s.Detail == "" {
			s.Detail = err.Error()
		}
		log.Printf("step failed: %s: %v", title, err)
	} else {
		s.Status = "ok"
	}
	r.Steps = append(r.Steps, s)
	return err
}

// Skip records a step that was intentionally not performed (e.g. opening an MR
// was skipped because the YAML mutation was a no-op).
func (r *Recorder) Skip(title, reason string) {
	r.Steps = append(r.Steps, Step{
		Title:  title,
		Status: "skip",
		Detail: reason,
	})
}

func (r *Recorder) TotalLabel() string {
	return formatDuration(time.Since(r.Start))
}

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return "<1ms"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}
