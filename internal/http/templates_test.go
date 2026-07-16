package http

import (
	"strings"
	"testing"
)

// TestPageTemplatesExecute renders every GET page template with the data shape
// the handlers pass, so a missing partial or bad field reference fails in tests
// rather than at request time (ExecuteTemplate errors are only logged).
func TestPageTemplatesExecute(t *testing.T) {
	data := map[string]any{
		"User":       "F920U2K",
		"Foundation": "fog",
		"CSRFToken":  "tok",
	}
	for _, name := range []string{
		"index.html",
		"add_user.html",
		"remove_user.html",
		"create_space.html",
		"manage_users.html",
		"manage_groups.html",
		"manage_org_users.html",
	} {
		var b strings.Builder
		if err := tpl.ExecuteTemplate(&b, name, data); err != nil {
			t.Errorf("execute %s: %v", name, err)
		}
		if b.Len() == 0 {
			t.Errorf("execute %s: empty output", name)
		}
	}
}
