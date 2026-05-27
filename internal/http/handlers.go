package http

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/example/cf-mgmt-portal/internal/cfapi"
	"github.com/example/cf-mgmt-portal/internal/gitlab"
	"github.com/example/cf-mgmt-portal/internal/mutate"
	"github.com/vmwarepivotallabs/cf-mgmt/config"
	"gopkg.in/yaml.v2"
)

const stateCookieName = "cf_mgmt_portal_oauth_state"

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	sess, _ := readSession(r, s.deps.SessionKey)
	renderTemplate(w, "index.html", map[string]any{
		"User":       sess.Username,
		"Foundation": s.deps.Foundation,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	state := randomToken()
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    state,
		Path:     "/auth/callback",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.deps.Auth.AuthURL(state), http.StatusSeeOther)
}

func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(stateCookieName)
	if err != nil || cookie.Value == "" || cookie.Value != r.URL.Query().Get("state") {
		renderError(w, http.StatusBadRequest, "invalid state")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		renderError(w, http.StatusBadRequest, "missing code")
		return
	}
	user, err := s.deps.Auth.Exchange(r.Context(), code)
	if err != nil {
		s.logErr(r, err)
		renderError(w, http.StatusBadGateway, "auth exchange failed")
		return
	}
	sess := session{
		GitLabID:  user.GitLabID,
		Username:  user.Username,
		Email:     user.Email,
		Expires:   time.Now().Add(sessionTTL),
		CSRFToken: randomToken(),
	}
	if err := setSessionCookie(w, s.deps.SessionKey, sess); err != nil {
		s.logErr(r, err)
		renderError(w, http.StatusInternalServerError, "session error")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleAddUserToRole(w http.ResponseWriter, r *http.Request) {
	sess, _ := readSession(r, s.deps.SessionKey)

	if r.Method == http.MethodGet {
		renderTemplate(w, "add_user.html", map[string]any{
			"User":       sess.Username,
			"Foundation": s.deps.Foundation,
			"CSRFToken":  sess.CSRFToken,
		})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		renderError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := r.ParseForm(); err != nil {
		renderError(w, http.StatusBadRequest, "bad form")
		return
	}
	if !verifyCSRF(r, sess) {
		renderError(w, http.StatusForbidden, "invalid CSRF token (your session may have expired — please sign in again)")
		return
	}

	org := strings.TrimSpace(r.FormValue("org"))
	space := strings.TrimSpace(r.FormValue("space"))
	role := mutate.Role(r.FormValue("role"))
	origin := mutate.Origin(r.FormValue("origin"))
	targetUser := strings.TrimSpace(r.FormValue("target_user"))
	if org == "" || space == "" || role == "" || origin == "" || targetUser == "" {
		renderError(w, http.StatusBadRequest, "missing required fields")
		return
	}

	ctx := r.Context()
	rec := NewRecorder()
	action := fmt.Sprintf("add %s as %s on %s/%s", targetUser, role, org, space)
	filePath := path.Join(s.deps.Foundation, org, space, "spaceConfig.yml")

	if err := rec.Do(fmt.Sprintf("Verifying you can manage %s", org), func() (string, error) {
		ok, err := s.deps.CFAPI.UserHasOrgRole(ctx, sess.Username, org, cfapi.RoleOrgManager)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("%s is not an OrgManager on %s", sess.Username, org)
		}
		return "OrgManager confirmed", nil
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	var current []byte
	if err := rec.Do("Reading "+filePath, func() (string, error) {
		b, err := s.deps.GitLab.GetFile(ctx, s.deps.ConfigRepoProject, s.deps.TargetBranch, filePath)
		if err != nil {
			return "", err
		}
		current = b
		return fmt.Sprintf("%d bytes", len(current)), nil
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	var updated []byte
	if err := rec.Do("Computing new YAML", func() (string, error) {
		b, err := mutate.AddUserToSpaceRole(current, role, origin, targetUser)
		if err != nil {
			return "", err
		}
		updated = b
		return "", nil
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	if string(updated) == string(current) {
		rec.Skip("Open merge request", "no change required")
		headline := fmt.Sprintf("%s is already %s on %s/%s — nothing to do.", targetUser, role, org, space)
		s.renderResult(w, sess, rec, action, headline, "", true)
		return
	}

	branch := fmt.Sprintf("portal/add-%s-%s-%s-%s", role, targetUser, space, randomToken()[:8])

	if err := rec.Do("Creating branch "+branch, func() (string, error) {
		return "", s.deps.GitLab.CreateBranch(ctx, s.deps.ConfigRepoProject, branch, s.deps.TargetBranch)
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	if err := rec.Do("Committing change", func() (string, error) {
		msg := fmt.Sprintf("portal: add %s as %s on %s/%s (by %s)", targetUser, role, org, space, sess.Username)
		return "", s.deps.GitLab.CommitFiles(ctx, s.deps.ConfigRepoProject, branch, msg, []gitlab.CommitAction{
			{Action: "update", FilePath: filePath, Content: string(updated)},
		})
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	// Best-effort: do not block on assignee lookup.
	var assignees []int
	_ = rec.Do(fmt.Sprintf("Looking up %s members", s.deps.PlatformTeamGroup), func() (string, error) {
		a, err := s.deps.GitLab.GroupMembers(ctx, s.deps.PlatformTeamGroup)
		if err != nil {
			return "", err
		}
		assignees = a
		return fmt.Sprintf("%d users", len(assignees)), nil
	})

	var mrURL string
	if err := rec.Do("Opening merge request", func() (string, error) {
		u, err := s.deps.GitLab.OpenMR(ctx, s.deps.ConfigRepoProject, gitlab.OpenMRRequest{
			SourceBranch: branch,
			TargetBranch: s.deps.TargetBranch,
			Title:        fmt.Sprintf("[portal] add %s as %s on %s/%s", targetUser, role, org, space),
			Description: fmt.Sprintf(
				"Generated by cf-mgmt-portal.\n\n"+
					"- **Foundation:** %s\n"+
					"- **Org:** %s\n"+
					"- **Space:** %s\n"+
					"- **Role:** %s\n"+
					"- **Origin:** %s\n"+
					"- **Target user:** %s\n"+
					"- **Requested by:** @%s\n",
				s.deps.Foundation, org, space, role, origin, targetUser, sess.Username,
			),
			AssigneeIDs: assignees,
		})
		if err != nil {
			return "", err
		}
		mrURL = u
		return "", nil
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	s.renderResult(w, sess, rec, action, "MR opened. Platform team will review and merge.", mrURL, true)
}

func (s *Server) handleCreateSpace(w http.ResponseWriter, r *http.Request) {
	sess, _ := readSession(r, s.deps.SessionKey)

	if r.Method == http.MethodGet {
		renderTemplate(w, "create_space.html", map[string]any{
			"User":       sess.Username,
			"Foundation": s.deps.Foundation,
			"CSRFToken":  sess.CSRFToken,
		})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		renderError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := r.ParseForm(); err != nil {
		renderError(w, http.StatusBadRequest, "bad form")
		return
	}
	if !verifyCSRF(r, sess) {
		renderError(w, http.StatusForbidden, "invalid CSRF token (your session may have expired — please sign in again)")
		return
	}

	org := strings.TrimSpace(r.FormValue("org"))
	space := strings.TrimSpace(r.FormValue("space"))
	addSelf := r.FormValue("add_self_as_manager") == "yes"
	if org == "" || space == "" {
		renderError(w, http.StatusBadRequest, "missing required fields")
		return
	}

	ctx := r.Context()
	rec := NewRecorder()
	action := fmt.Sprintf("create space %s/%s", org, space)
	spacesPath := path.Join(s.deps.Foundation, org, "spaces.yml")

	if err := rec.Do(fmt.Sprintf("Verifying you can manage %s", org), func() (string, error) {
		ok, err := s.deps.CFAPI.UserHasOrgRole(ctx, sess.Username, org, cfapi.RoleOrgManager)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("%s is not an OrgManager on %s", sess.Username, org)
		}
		return "OrgManager confirmed", nil
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	var currentSpaces []byte
	if err := rec.Do("Reading "+spacesPath, func() (string, error) {
		b, err := s.deps.GitLab.GetFile(ctx, s.deps.ConfigRepoProject, s.deps.TargetBranch, spacesPath)
		if err != nil {
			return "", err
		}
		currentSpaces = b
		return fmt.Sprintf("%d bytes", len(currentSpaces)), nil
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	var defaults *config.SpaceConfig
	_ = rec.Do("Reading spaceDefaults.yml (best-effort)", func() (string, error) {
		p := path.Join(s.deps.Foundation, "spaceDefaults.yml")
		b, err := s.deps.GitLab.GetFile(ctx, s.deps.ConfigRepoProject, s.deps.TargetBranch, p)
		if err != nil {
			return "missing — using empty defaults", nil
		}
		var d config.SpaceConfig
		if yaml.Unmarshal(b, &d) != nil {
			return "unparseable — using empty defaults", nil
		}
		defaults = &d
		return "loaded", nil
	})

	var changes []mutate.FileChange
	if err := rec.Do("Computing file changes", func() (string, error) {
		c, err := mutate.CreateSpace(currentSpaces, s.deps.Foundation, org, space, defaults)
		if err != nil {
			return "", err
		}
		changes = c
		if len(changes) == 0 {
			return "no changes", nil
		}
		return fmt.Sprintf("%d files", len(changes)), nil
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	if len(changes) == 0 {
		rec.Skip("Open merge request", "space already exists")
		headline := fmt.Sprintf("Space %s already exists in %s — nothing to do.", space, org)
		s.renderResult(w, sess, rec, action, headline, "", true)
		return
	}

	if addSelf {
		spaceConfigPath := path.Join(s.deps.Foundation, org, space, "spaceConfig.yml")
		_ = rec.Do(fmt.Sprintf("Adding you (%s) as space-manager", sess.Username), func() (string, error) {
			for i, c := range changes {
				if c.Path != spaceConfigPath {
					continue
				}
				updated, err := mutate.AddUserToSpaceRole(c.Content, mutate.RoleManager, mutate.OriginLDAP, sess.Username)
				if err != nil {
					return "", err
				}
				changes[i].Content = updated
				return "", nil
			}
			return "spaceConfig.yml not in changes — skipped", nil
		})
	}

	branch := fmt.Sprintf("portal/create-space-%s-%s-%s", org, space, randomToken()[:8])

	if err := rec.Do("Creating branch "+branch, func() (string, error) {
		return "", s.deps.GitLab.CreateBranch(ctx, s.deps.ConfigRepoProject, branch, s.deps.TargetBranch)
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	actions := make([]gitlab.CommitAction, 0, len(changes))
	for _, c := range changes {
		act := "update"
		if c.IsNew {
			act = "create"
		}
		actions = append(actions, gitlab.CommitAction{
			Action: act, FilePath: c.Path, Content: string(c.Content),
		})
	}

	if err := rec.Do("Committing change", func() (string, error) {
		msg := fmt.Sprintf("portal: create space %s/%s (by %s)", org, space, sess.Username)
		return "", s.deps.GitLab.CommitFiles(ctx, s.deps.ConfigRepoProject, branch, msg, actions)
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	var assignees []int
	_ = rec.Do(fmt.Sprintf("Looking up %s members", s.deps.PlatformTeamGroup), func() (string, error) {
		a, err := s.deps.GitLab.GroupMembers(ctx, s.deps.PlatformTeamGroup)
		if err != nil {
			return "", err
		}
		assignees = a
		return fmt.Sprintf("%d users", len(assignees)), nil
	})

	managerNote := ""
	if addSelf {
		managerNote = fmt.Sprintf("- **Initial space-manager:** @%s\n", sess.Username)
	}
	var mrURL string
	if err := rec.Do("Opening merge request", func() (string, error) {
		u, err := s.deps.GitLab.OpenMR(ctx, s.deps.ConfigRepoProject, gitlab.OpenMRRequest{
			SourceBranch: branch,
			TargetBranch: s.deps.TargetBranch,
			Title:        fmt.Sprintf("[portal] create space %s/%s", org, space),
			Description: fmt.Sprintf(
				"Generated by cf-mgmt-portal.\n\n"+
					"- **Foundation:** %s\n"+
					"- **Org:** %s\n"+
					"- **New space:** %s\n"+
					"%s"+
					"- **Requested by:** @%s\n",
				s.deps.Foundation, org, space, managerNote, sess.Username,
			),
			AssigneeIDs: assignees,
		})
		if err != nil {
			return "", err
		}
		mrURL = u
		return "", nil
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	s.renderResult(w, sess, rec, action, "MR opened. Platform team will review and merge.", mrURL, true)
}

// renderResult renders the terminal-style result page. status is 200 on
// success, 502 otherwise (the most common reason for failure here is an
// upstream — GitLab or CF API — returning an error).
func (s *Server) renderResult(w http.ResponseWriter, sess session, rec *Recorder, action, headline, mrURL string, success bool) {
	status := http.StatusOK
	if !success {
		status = http.StatusBadGateway
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	err := tpl.ExecuteTemplate(w, "result.html", map[string]any{
		"User":       sess.Username,
		"Foundation": s.deps.Foundation,
		"Action":     action,
		"Headline":   headline,
		"MRURL":      mrURL,
		"Success":    success,
		"Steps":      rec.Steps,
		"Total":      rec.TotalLabel(),
	})
	if err != nil {
		log.Printf("render result.html: %v", err)
	}
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// verifyCSRF checks that the form's csrf_token field matches the session's
// CSRF token using constant-time comparison. Empty tokens never match.
// Caller must have called r.ParseForm() first.
func verifyCSRF(r *http.Request, sess session) bool {
	got := r.FormValue("csrf_token")
	if got == "" || sess.CSRFToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) == 1
}
