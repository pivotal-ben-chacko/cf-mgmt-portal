package http

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/example/cf-mgmt-portal/internal/gitlab"
)

// fakeGitLab captures the write calls the portal makes so tests can assert on
// the commit and MR payloads. File paths are matched on the escaped URL since
// project IDs contain encoded slashes.
type fakeGitLab struct {
	files    map[string]string // repo file path -> content
	commits  []map[string]any
	mrBodies []map[string]any
}

func (f *fakeGitLab) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.EscapedPath()
		switch {
		case r.Method == http.MethodGet && strings.Contains(p, "/repository/files/") && strings.HasSuffix(p, "/raw"):
			enc := p[strings.Index(p, "/repository/files/")+len("/repository/files/") : len(p)-len("/raw")]
			path, err := url.PathUnescape(enc)
			if err != nil {
				t.Errorf("bad file path %q: %v", enc, err)
			}
			content, ok := f.files[path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = io.WriteString(w, content)
		case r.Method == http.MethodPost && strings.Contains(p, "/repository/branches"):
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{}`)
		case r.Method == http.MethodPost && strings.Contains(p, "/repository/commits"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.commits = append(f.commits, body)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{}`)
		case r.Method == http.MethodGet && strings.Contains(p, "/members/all"):
			_, _ = io.WriteString(w, `[{"id":7}]`)
		case r.Method == http.MethodPost && strings.Contains(p, "/merge_requests"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mrBodies = append(f.mrBodies, body)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"web_url":"http://gitlab.test/mr/1"}`)
		default:
			t.Errorf("unexpected gitlab request: %s %s", r.Method, p)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// newManageUsersServer wires a Server with a fake GitLab; the session user is
// a portal admin so the CFAPI authz check is skipped (CFAPI is nil).
func newManageUsersServer(t *testing.T, fake *fakeGitLab) (http.Handler, *http.Cookie) {
	t.Helper()
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)
	key := []byte("0123456789abcdef0123456789abcdef")
	h := NewServer(Deps{
		SessionKey:        key,
		Foundation:        "fog",
		ConfigRepoProject: "Global/ofs-lowers/fog-cf-mgmt",
		PlatformTeamGroup: "platform-cf-admins",
		GitLab:            gitlab.NewClient(srv.URL, "tok"),
		AdminUsers:        []string{"F920U2K"},
	})
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

func postManageUsers(t *testing.T, h http.Handler, cookie *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/actions/manage-users", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestManageUsers_MultiSpaceSingleMR(t *testing.T) {
	fake := &fakeGitLab{files: map[string]string{
		"fog/system/dev/spaceConfig.yml": "org: system\nspace: dev\nspace-developer:\n  ldap_users:\n  - olddev\n",
		"fog/system/qa/spaceConfig.yml":  "org: system\nspace: qa\nspace-manager:\n  ldap_users:\n  - boss\n",
	}}
	h, cookie := newManageUsersServer(t, fake)

	changes := `[
		{"space":"dev","ops":[{"user":"newdev","origin":"ldap","role":"developer","action":"add"}]},
		{"space":"qa","ops":[{"user":"boss","origin":"ldap","role":"manager","action":"remove"}]}
	]`
	w := postManageUsers(t, h, cookie, url.Values{
		"csrf_token": {"tok"}, "org": {"system"}, "changes": {changes},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d:\n%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "http://gitlab.test/mr/1") {
		t.Errorf("expected MR URL in response")
	}

	if len(fake.commits) != 1 {
		t.Fatalf("expected exactly 1 commit, got %d", len(fake.commits))
	}
	actions := fake.commits[0]["actions"].([]any)
	if len(actions) != 2 {
		t.Fatalf("expected 2 file actions in the single commit, got %d", len(actions))
	}
	paths := map[string]string{}
	for _, a := range actions {
		m := a.(map[string]any)
		paths[m["file_path"].(string)] = m["content"].(string)
	}
	if c, ok := paths["fog/system/dev/spaceConfig.yml"]; !ok || !strings.Contains(c, "- newdev") {
		t.Errorf("dev spaceConfig missing or lacks newdev: %v", paths)
	}
	if c, ok := paths["fog/system/qa/spaceConfig.yml"]; !ok || strings.Contains(c, "- boss") {
		t.Errorf("qa spaceConfig missing or still contains boss: %v", paths)
	}

	if len(fake.mrBodies) != 1 {
		t.Fatalf("expected exactly 1 MR, got %d", len(fake.mrBodies))
	}
	desc := fake.mrBodies[0]["description"].(string)
	for _, want := range []string{"**dev**", "**qa**", "newdev", "boss"} {
		if !strings.Contains(desc, want) {
			t.Errorf("MR description missing %q:\n%s", want, desc)
		}
	}
}

func TestManageUsers_NoOpSpaceExcludedFromCommit(t *testing.T) {
	fake := &fakeGitLab{files: map[string]string{
		"fog/system/dev/spaceConfig.yml": "org: system\nspace: dev\nspace-developer:\n  ldap_users:\n  - present\n",
		"fog/system/qa/spaceConfig.yml":  "org: system\nspace: qa\nspace-manager:\n  ldap_users:\n  - boss\n",
	}}
	h, cookie := newManageUsersServer(t, fake)

	// dev batch is a no-op (user already present); only qa should be committed.
	changes := `[
		{"space":"dev","ops":[{"user":"present","origin":"ldap","role":"developer","action":"add"}]},
		{"space":"qa","ops":[{"user":"newmgr","origin":"ldap","role":"manager","action":"add"}]}
	]`
	w := postManageUsers(t, h, cookie, url.Values{
		"csrf_token": {"tok"}, "org": {"system"}, "changes": {changes},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(fake.commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(fake.commits))
	}
	actions := fake.commits[0]["actions"].([]any)
	if len(actions) != 1 {
		t.Fatalf("expected only the qa file in the commit, got %d actions", len(actions))
	}
	if fp := actions[0].(map[string]any)["file_path"].(string); fp != "fog/system/qa/spaceConfig.yml" {
		t.Errorf("unexpected file in commit: %s", fp)
	}
}

func TestManageUsers_AllNoOpSkipsMR(t *testing.T) {
	fake := &fakeGitLab{files: map[string]string{
		"fog/system/dev/spaceConfig.yml": "org: system\nspace: dev\nspace-developer:\n  ldap_users:\n  - present\n",
	}}
	h, cookie := newManageUsersServer(t, fake)

	changes := `[{"space":"dev","ops":[{"user":"present","origin":"ldap","role":"developer","action":"add"}]}]`
	w := postManageUsers(t, h, cookie, url.Values{
		"csrf_token": {"tok"}, "org": {"system"}, "changes": {changes},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(fake.commits) != 0 || len(fake.mrBodies) != 0 {
		t.Errorf("expected no commit and no MR for all-no-op submit")
	}
	if !strings.Contains(w.Body.String(), "Nothing to change") {
		t.Errorf("expected no-op headline in response")
	}
}

func TestManageUsers_RejectsTraversalSpaceName(t *testing.T) {
	fake := &fakeGitLab{files: map[string]string{}}
	h, cookie := newManageUsersServer(t, fake)

	changes := `[{"space":"../other-org/dev","ops":[{"user":"x","origin":"ldap","role":"developer","action":"add"}]}]`
	w := postManageUsers(t, h, cookie, url.Values{
		"csrf_token": {"tok"}, "org": {"system"}, "changes": {changes},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for traversal space name, got %d", w.Code)
	}

	w = postManageUsers(t, h, cookie, url.Values{
		"csrf_token": {"tok"}, "org": {"../secret"}, "changes": {`[{"space":"dev","ops":[{"user":"x","origin":"ldap","role":"developer","action":"add"}]}]`},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for traversal org name, got %d", w.Code)
	}
}

func TestParseSpaceChanges_MergesDuplicateSpaces(t *testing.T) {
	raw := `[
		{"space":"dev","ops":[{"user":"a","origin":"ldap","role":"developer","action":"add"}]},
		{"space":"","ops":[{"user":"ghost","origin":"ldap","role":"developer","action":"add"}]},
		{"space":"empty","ops":[]},
		{"space":"dev","ops":[{"user":"b","origin":"ldap","role":"manager","action":"add"}]}
	]`
	changes, total, err := parseSpaceChanges(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Space != "dev" || len(changes[0].Ops) != 2 {
		t.Fatalf("expected merged dev entry with 2 ops, got %+v", changes)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 (dropped entries don't count)", total)
	}
}
