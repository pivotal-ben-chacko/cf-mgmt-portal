package http

import (
	"log"
	"net/http"

	"github.com/example/cf-mgmt-portal/internal/auth"
	"github.com/example/cf-mgmt-portal/internal/cfapi"
	"github.com/example/cf-mgmt-portal/internal/gitlab"
)

// Deps groups the collaborators the HTTP layer needs.
//
// Foundation is the subdirectory in the config repo (e.g. "fog") where the
// org/space tree lives. v1 supports a single foundation; multi-foundation would
// switch this to a map and the CFAPI/Foundation fields would be per-foundation.
type Deps struct {
	Auth              *auth.Client
	CFAPI             *cfapi.Client
	GitLab            *gitlab.Client
	ConfigRepoProject string
	PlatformTeamGroup string
	SessionKey        []byte
	TargetBranch      string // MR base; defaults to "development"
	Foundation        string

	// AdminUsers lists usernames exempt from the OrgManager authz check (e.g.
	// the UAA admin user). They can open MRs for any org; the platform team's
	// MR review remains the write gate. Empty means no exemptions.
	AdminUsers []string
}

type Server struct {
	deps Deps
	mux  *http.ServeMux
}

func NewServer(deps Deps) http.Handler {
	if deps.TargetBranch == "" {
		deps.TargetBranch = "development"
	}
	s := &Server{deps: deps, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/auth/login", s.handleLogin)
	s.mux.HandleFunc("/auth/callback", s.handleCallback)
	s.mux.HandleFunc("/auth/logout", s.handleLogout)
	s.mux.HandleFunc("/actions/add-user-to-role", s.requireAuth(s.handleAddUserToRole))
	s.mux.HandleFunc("/actions/remove-user-from-role", s.requireAuth(s.handleRemoveUserFromRole))
	s.mux.HandleFunc("/actions/create-space", s.requireAuth(s.handleCreateSpace))
	s.mux.HandleFunc("/actions/manage-users", s.requireAuth(s.handleManageUsers))
	s.mux.HandleFunc("/actions/manage-groups", s.requireAuth(s.handleManageGroups))
	s.mux.HandleFunc("/api/orgs", s.requireAuth(s.handleAPIOrgs))
	s.mux.HandleFunc("/api/spaces", s.requireAuth(s.handleAPISpaces))
	s.mux.HandleFunc("/api/space-users", s.requireAuth(s.handleAPISpaceUsers))
	s.mux.HandleFunc("/api/space-groups", s.requireAuth(s.handleAPISpaceGroups))
	s.mux.HandleFunc("/assets/", handleAsset)
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
}

func (s *Server) requireAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := readSession(r, s.deps.SessionKey); err != nil {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
		h(w, r)
	}
}

func (s *Server) logErr(r *http.Request, err error) {
	log.Printf("error %s %s: %v", r.Method, r.URL.Path, err)
}
