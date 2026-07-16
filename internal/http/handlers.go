package http

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
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

// isPortalAdmin reports whether username is exempt from the OrgManager check.
func (s *Server) isPortalAdmin(username string) bool {
	for _, a := range s.deps.AdminUsers {
		if a == username {
			return true
		}
	}
	return false
}

// inAdminGroup reports whether username is a member of any of the configured
// admin UAA groups. The lookup is best-effort: a failure (e.g. the service
// account lacks scim.read) is recorded in the step log and treated as "not an
// admin", so the normal OrgManager check still runs.
func (s *Server) inAdminGroup(ctx context.Context, rec *Recorder, username string) bool {
	member := false
	_ = rec.Do("Checking admin group membership", func() (string, error) {
		groups, err := s.deps.CFAPI.UserGroups(ctx, username)
		if err != nil {
			return "", err
		}
		for _, g := range groups {
			for _, ag := range s.deps.AdminGroups {
				if g == ag {
					member = true
					return "member of " + g, nil
				}
			}
		}
		return "not in " + strings.Join(s.deps.AdminGroups, ", "), nil
	})
	return member
}

// verifyOrgManager runs the standard authz step for a mutating action: the
// logged-in user must hold OrgManager on org via the CF API, unless they are
// a configured portal admin (by username or UAA group membership), in which
// case the check is skipped and recorded as such in the step log.
func (s *Server) verifyOrgManager(ctx context.Context, rec *Recorder, sess session, org string) error {
	title := fmt.Sprintf("Verifying you can manage %s", org)
	if s.isPortalAdmin(sess.Username) {
		rec.Skip(title, sess.Username+" is a portal admin")
		return nil
	}
	if len(s.deps.AdminGroups) > 0 && s.inAdminGroup(ctx, rec, sess.Username) {
		rec.Skip(title, sess.Username+" is in an admin group")
		return nil
	}
	return rec.Do(title, func() (string, error) {
		ok, err := s.deps.CFAPI.UserHasOrgRole(ctx, sess.Username, org, cfapi.RoleOrgManager)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("%s is not an OrgManager on %s", sess.Username, org)
		}
		return "OrgManager confirmed", nil
	})
}

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

	if err := s.verifyOrgManager(ctx, rec, sess, org); err != nil {
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

func (s *Server) handleRemoveUserFromRole(w http.ResponseWriter, r *http.Request) {
	sess, _ := readSession(r, s.deps.SessionKey)

	if r.Method == http.MethodGet {
		renderTemplate(w, "remove_user.html", map[string]any{
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
	action := fmt.Sprintf("remove %s as %s on %s/%s", targetUser, role, org, space)
	filePath := path.Join(s.deps.Foundation, org, space, "spaceConfig.yml")

	if err := s.verifyOrgManager(ctx, rec, sess, org); err != nil {
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
		b, err := mutate.RemoveUserFromSpaceRole(current, role, origin, targetUser)
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
		headline := fmt.Sprintf("%s is not %s on %s/%s — nothing to do.", targetUser, role, org, space)
		s.renderResult(w, sess, rec, action, headline, "", true)
		return
	}

	branch := fmt.Sprintf("portal/remove-%s-%s-%s-%s", role, targetUser, space, randomToken()[:8])

	if err := rec.Do("Creating branch "+branch, func() (string, error) {
		return "", s.deps.GitLab.CreateBranch(ctx, s.deps.ConfigRepoProject, branch, s.deps.TargetBranch)
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	if err := rec.Do("Committing change", func() (string, error) {
		msg := fmt.Sprintf("portal: remove %s as %s on %s/%s (by %s)", targetUser, role, org, space, sess.Username)
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
			Title:        fmt.Sprintf("[portal] remove %s as %s on %s/%s", targetUser, role, org, space),
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

func (s *Server) handleManageUsers(w http.ResponseWriter, r *http.Request) {
	sess, _ := readSession(r, s.deps.SessionKey)

	if r.Method == http.MethodGet {
		renderTemplate(w, "manage_users.html", map[string]any{
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
	if org == "" || space == "" {
		renderError(w, http.StatusBadRequest, "missing org or space")
		return
	}
	var ops []mutate.Op
	if err := json.Unmarshal([]byte(r.FormValue("ops")), &ops); err != nil {
		renderError(w, http.StatusBadRequest, "could not parse changes")
		return
	}
	if len(ops) == 0 {
		renderError(w, http.StatusBadRequest, "no changes selected")
		return
	}

	ctx := r.Context()
	rec := NewRecorder()
	action := fmt.Sprintf("manage users on %s/%s (%d change(s))", org, space, len(ops))
	filePath := path.Join(s.deps.Foundation, org, space, "spaceConfig.yml")

	if err := s.verifyOrgManager(ctx, rec, sess, org); err != nil {
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
		b, err := mutate.ApplySpaceUserOps(current, ops)
		if err != nil {
			return "", err
		}
		updated = b
		return fmt.Sprintf("%d change(s)", len(ops)), nil
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	if string(updated) == string(current) {
		rec.Skip("Open merge request", "no change required")
		headline := fmt.Sprintf("Nothing to change on %s/%s — the space already matches your selection.", org, space)
		s.renderResult(w, sess, rec, action, headline, "", true)
		return
	}

	branch := fmt.Sprintf("portal/manage-users-%s-%s-%s", org, space, randomToken()[:8])

	if err := rec.Do("Creating branch "+branch, func() (string, error) {
		return "", s.deps.GitLab.CreateBranch(ctx, s.deps.ConfigRepoProject, branch, s.deps.TargetBranch)
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	if err := rec.Do("Committing change", func() (string, error) {
		msg := fmt.Sprintf("portal: manage users on %s/%s (by %s)", org, space, sess.Username)
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
			Title:        fmt.Sprintf("[portal] manage users on %s/%s", org, space),
			Description: fmt.Sprintf(
				"Generated by cf-mgmt-portal.\n\n"+
					"- **Foundation:** %s\n"+
					"- **Org:** %s\n"+
					"- **Space:** %s\n"+
					"- **Requested by:** @%s\n\n"+
					"### Changes\n%s",
				s.deps.Foundation, org, space, sess.Username, opsSummary(ops, "space"),
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

// opsSummary renders the batch of role changes as a markdown list for the MR
// body. scope is the role-key prefix: "space" or "org".
func opsSummary(ops []mutate.Op, scope string) string {
	var b strings.Builder
	for _, op := range ops {
		verb := "Add"
		if op.Action == "remove" {
			verb = "Remove"
		}
		fmt.Fprintf(&b, "- %s **%s** as %s-%s (%s)\n", verb, op.User, scope, op.Role, op.Origin)
	}
	return b.String()
}

func (s *Server) handleManageOrgUsers(w http.ResponseWriter, r *http.Request) {
	sess, _ := readSession(r, s.deps.SessionKey)

	if r.Method == http.MethodGet {
		renderTemplate(w, "manage_org_users.html", map[string]any{
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
	if org == "" {
		renderError(w, http.StatusBadRequest, "missing org")
		return
	}
	var ops []mutate.Op
	if err := json.Unmarshal([]byte(r.FormValue("ops")), &ops); err != nil {
		renderError(w, http.StatusBadRequest, "could not parse changes")
		return
	}
	if len(ops) == 0 {
		renderError(w, http.StatusBadRequest, "no changes selected")
		return
	}

	ctx := r.Context()
	rec := NewRecorder()
	action := fmt.Sprintf("manage org users on %s (%d change(s))", org, len(ops))
	filePath := path.Join(s.deps.Foundation, org, "orgConfig.yml")

	if err := s.verifyOrgManager(ctx, rec, sess, org); err != nil {
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
		b, err := mutate.ApplyOrgUserOps(current, ops)
		if err != nil {
			return "", err
		}
		updated = b
		return fmt.Sprintf("%d change(s)", len(ops)), nil
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	if string(updated) == string(current) {
		rec.Skip("Open merge request", "no change required")
		headline := fmt.Sprintf("Nothing to change on %s — the org already matches your selection.", org)
		s.renderResult(w, sess, rec, action, headline, "", true)
		return
	}

	branch := fmt.Sprintf("portal/manage-org-users-%s-%s", org, randomToken()[:8])

	if err := rec.Do("Creating branch "+branch, func() (string, error) {
		return "", s.deps.GitLab.CreateBranch(ctx, s.deps.ConfigRepoProject, branch, s.deps.TargetBranch)
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	if err := rec.Do("Committing change", func() (string, error) {
		msg := fmt.Sprintf("portal: manage org users on %s (by %s)", org, sess.Username)
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
			Title:        fmt.Sprintf("[portal] manage org users on %s", org),
			Description: fmt.Sprintf(
				"Generated by cf-mgmt-portal.\n\n"+
					"- **Foundation:** %s\n"+
					"- **Org:** %s\n"+
					"- **Requested by:** @%s\n\n"+
					"### Changes (org roles)\n%s",
				s.deps.Foundation, org, sess.Username, opsSummary(ops, "org"),
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

func (s *Server) handleManageGroups(w http.ResponseWriter, r *http.Request) {
	sess, _ := readSession(r, s.deps.SessionKey)

	if r.Method == http.MethodGet {
		renderTemplate(w, "manage_groups.html", map[string]any{
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
	if org == "" || space == "" {
		renderError(w, http.StatusBadRequest, "missing org or space")
		return
	}
	var ops []mutate.GroupOp
	if err := json.Unmarshal([]byte(r.FormValue("ops")), &ops); err != nil {
		renderError(w, http.StatusBadRequest, "could not parse changes")
		return
	}
	if len(ops) == 0 {
		renderError(w, http.StatusBadRequest, "no changes selected")
		return
	}

	ctx := r.Context()
	rec := NewRecorder()
	action := fmt.Sprintf("manage groups on %s/%s (%d change(s))", org, space, len(ops))
	filePath := path.Join(s.deps.Foundation, org, space, "spaceConfig.yml")

	if err := s.verifyOrgManager(ctx, rec, sess, org); err != nil {
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
		b, err := mutate.ApplySpaceGroupOps(current, ops)
		if err != nil {
			return "", err
		}
		updated = b
		return fmt.Sprintf("%d change(s)", len(ops)), nil
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	if string(updated) == string(current) {
		rec.Skip("Open merge request", "no change required")
		headline := fmt.Sprintf("Nothing to change on %s/%s — the space already matches your selection.", org, space)
		s.renderResult(w, sess, rec, action, headline, "", true)
		return
	}

	branch := fmt.Sprintf("portal/manage-groups-%s-%s-%s", org, space, randomToken()[:8])

	if err := rec.Do("Creating branch "+branch, func() (string, error) {
		return "", s.deps.GitLab.CreateBranch(ctx, s.deps.ConfigRepoProject, branch, s.deps.TargetBranch)
	}); err != nil {
		s.renderResult(w, sess, rec, action, "", "", false)
		return
	}

	if err := rec.Do("Committing change", func() (string, error) {
		msg := fmt.Sprintf("portal: manage groups on %s/%s (by %s)", org, space, sess.Username)
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
			Title:        fmt.Sprintf("[portal] manage groups on %s/%s", org, space),
			Description: fmt.Sprintf(
				"Generated by cf-mgmt-portal.\n\n"+
					"- **Foundation:** %s\n"+
					"- **Org:** %s\n"+
					"- **Space:** %s\n"+
					"- **Requested by:** @%s\n\n"+
					"### Changes (LDAP groups)\n%s",
				s.deps.Foundation, org, space, sess.Username, groupOpsSummary(ops),
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

// groupOpsSummary renders the batch of LDAP-group changes as a markdown list for the MR body.
func groupOpsSummary(ops []mutate.GroupOp) string {
	var b strings.Builder
	for _, op := range ops {
		verb := "Add"
		if op.Action == "remove" {
			verb = "Remove"
		}
		fmt.Fprintf(&b, "- %s group **%s** as space-%s\n", verb, op.Group, op.Role)
	}
	return b.String()
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

	if err := s.verifyOrgManager(ctx, rec, sess, org); err != nil {
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

// handleAPIOrgs returns the orgs declared in the foundation's orgs.yml. Read
// endpoints are open to any authenticated user (the config is reviewable via
// MR anyway); the write path still enforces OrgManager.
func (s *Server) handleAPIOrgs(w http.ResponseWriter, r *http.Request) {
	p := path.Join(s.deps.Foundation, "orgs.yml")
	b, err := s.deps.GitLab.GetFile(r.Context(), s.deps.ConfigRepoProject, s.deps.TargetBranch, p)
	if err != nil {
		s.logErr(r, err)
		writeJSONError(w, http.StatusBadGateway, "could not read orgs.yml")
		return
	}
	var cfg config.Orgs
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		writeJSONError(w, http.StatusBadGateway, "could not parse orgs.yml")
		return
	}
	writeJSON(w, map[string]any{"orgs": cfg.Orgs})
}

// handleAPISpaces returns the spaces declared in <org>/spaces.yml.
func (s *Server) handleAPISpaces(w http.ResponseWriter, r *http.Request) {
	org := strings.TrimSpace(r.URL.Query().Get("org"))
	if org == "" {
		writeJSONError(w, http.StatusBadRequest, "missing org")
		return
	}
	p := path.Join(s.deps.Foundation, org, "spaces.yml")
	b, err := s.deps.GitLab.GetFile(r.Context(), s.deps.ConfigRepoProject, s.deps.TargetBranch, p)
	if err != nil {
		s.logErr(r, err)
		writeJSONError(w, http.StatusBadGateway, "could not read spaces.yml")
		return
	}
	var cfg config.Spaces
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		writeJSONError(w, http.StatusBadGateway, "could not parse spaces.yml")
		return
	}
	writeJSON(w, map[string]any{"spaces": cfg.Spaces})
}

// handleAPISpaceUsers returns the users in each role of a space, grouped by origin.
func (s *Server) handleAPISpaceUsers(w http.ResponseWriter, r *http.Request) {
	org := strings.TrimSpace(r.URL.Query().Get("org"))
	space := strings.TrimSpace(r.URL.Query().Get("space"))
	if org == "" || space == "" {
		writeJSONError(w, http.StatusBadRequest, "missing org or space")
		return
	}
	p := path.Join(s.deps.Foundation, org, space, "spaceConfig.yml")
	b, err := s.deps.GitLab.GetFile(r.Context(), s.deps.ConfigRepoProject, s.deps.TargetBranch, p)
	if err != nil {
		s.logErr(r, err)
		writeJSONError(w, http.StatusBadGateway, "could not read spaceConfig.yml")
		return
	}
	roles, err := mutate.ListSpaceUsers(b)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "could not parse spaceConfig.yml")
		return
	}
	writeJSON(w, map[string]any{"roles": roles})
}

// handleAPIOrgUsers returns the users in each org role, grouped by origin.
func (s *Server) handleAPIOrgUsers(w http.ResponseWriter, r *http.Request) {
	org := strings.TrimSpace(r.URL.Query().Get("org"))
	if org == "" {
		writeJSONError(w, http.StatusBadRequest, "missing org")
		return
	}
	p := path.Join(s.deps.Foundation, org, "orgConfig.yml")
	b, err := s.deps.GitLab.GetFile(r.Context(), s.deps.ConfigRepoProject, s.deps.TargetBranch, p)
	if err != nil {
		s.logErr(r, err)
		writeJSONError(w, http.StatusBadGateway, "could not read orgConfig.yml")
		return
	}
	roles, err := mutate.ListOrgUsers(b)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "could not parse orgConfig.yml")
		return
	}
	writeJSON(w, map[string]any{"roles": roles})
}

// handleAPISpaceGroups returns the LDAP groups bound to each role of a space.
func (s *Server) handleAPISpaceGroups(w http.ResponseWriter, r *http.Request) {
	org := strings.TrimSpace(r.URL.Query().Get("org"))
	space := strings.TrimSpace(r.URL.Query().Get("space"))
	if org == "" || space == "" {
		writeJSONError(w, http.StatusBadRequest, "missing org or space")
		return
	}
	p := path.Join(s.deps.Foundation, org, space, "spaceConfig.yml")
	b, err := s.deps.GitLab.GetFile(r.Context(), s.deps.ConfigRepoProject, s.deps.TargetBranch, p)
	if err != nil {
		s.logErr(r, err)
		writeJSONError(w, http.StatusBadGateway, "could not read spaceConfig.yml")
		return
	}
	roles, err := mutate.ListSpaceGroups(b)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "could not parse spaceConfig.yml")
		return
	}
	writeJSON(w, map[string]any{"roles": roles})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
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
