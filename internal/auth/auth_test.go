package auth

import (
	"strings"
	"testing"
)

func TestAuthURL_IncludesStateAndScope(t *testing.T) {
	c, err := NewGitLabOAuth("https://gitlab.example/", "cid", "csecret", "https://portal/cb")
	if err != nil {
		t.Fatal(err)
	}
	u := c.AuthURL("xyz123")
	if !strings.Contains(u, "https://gitlab.example/oauth/authorize") {
		t.Errorf("wrong host: %s", u)
	}
	for _, want := range []string{"state=xyz123", "scope=read_user", "client_id=cid", "response_type=code"} {
		if !strings.Contains(u, want) {
			t.Errorf("missing %q in %s", want, u)
		}
	}
}
