package main

import (
	"log"
	"net/http"
	"os"

	"github.com/example/cf-mgmt-portal/internal/auth"
	"github.com/example/cf-mgmt-portal/internal/cfapi"
	"github.com/example/cf-mgmt-portal/internal/gitlab"
	portalhttp "github.com/example/cf-mgmt-portal/internal/http"
)

type config struct {
	PortalURL    string
	SessionKey   string // raw secret, >= 32 chars
	Foundation   string // subdirectory in the config repo, e.g. "fog"
	TargetBranch string // optional, defaults to "development"

	GitLabURL         string
	GitLabToken       string
	ConfigRepoProject string
	PlatformTeamGroup string

	CFAPIURL string
	UAAURL   string
	// Service-account client (client_credentials) for CF API authz checks.
	UAAClientID     string
	UAAClientSecret string
	// Login client (authorization_code, scope openid) for user sign-in.
	UAALoginClientID     string
	UAALoginClientSecret string
}

func main() {
	cfg := loadConfig()

	authClient, err := auth.NewUAAOAuth(
		cfg.UAAURL,
		cfg.UAALoginClientID,
		cfg.UAALoginClientSecret,
		cfg.PortalURL+"/auth/callback",
	)
	if err != nil {
		log.Fatalf("auth init: %v", err)
	}

	cfClient := cfapi.NewClient(cfg.CFAPIURL, cfg.UAAURL, cfg.UAAClientID, cfg.UAAClientSecret)
	glClient := gitlab.NewClient(cfg.GitLabURL, cfg.GitLabToken)

	srv := portalhttp.NewServer(portalhttp.Deps{
		Auth:              authClient,
		CFAPI:             cfClient,
		GitLab:            glClient,
		ConfigRepoProject: cfg.ConfigRepoProject,
		PlatformTeamGroup: cfg.PlatformTeamGroup,
		SessionKey:        []byte(cfg.SessionKey),
		TargetBranch:      cfg.TargetBranch,
		Foundation:        cfg.Foundation,
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("cf-mgmt-portal listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, srv))
}

func loadConfig() config {
	c := config{
		PortalURL:               requireEnv("PORTAL_URL"),
		SessionKey:              requireEnv("SESSION_KEY"),
		Foundation:              requireEnv("FOUNDATION"),
		TargetBranch:            os.Getenv("TARGET_BRANCH"),
		GitLabURL:            requireEnv("GITLAB_URL"),
		GitLabToken:          requireEnv("GITLAB_TOKEN"),
		ConfigRepoProject:    requireEnv("CONFIG_REPO_PROJECT"),
		PlatformTeamGroup:    requireEnv("PLATFORM_TEAM_GROUP"),
		CFAPIURL:             requireEnv("CF_API_URL"),
		UAAURL:               requireEnv("UAA_URL"),
		UAAClientID:          requireEnv("UAA_CLIENT_ID"),
		UAAClientSecret:      requireEnv("UAA_CLIENT_SECRET"),
		UAALoginClientID:     requireEnv("UAA_LOGIN_CLIENT_ID"),
		UAALoginClientSecret: requireEnv("UAA_LOGIN_CLIENT_SECRET"),
	}
	if len(c.SessionKey) < 32 {
		log.Fatal("SESSION_KEY must be at least 32 characters")
	}
	return c
}

func requireEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("env var %s is required", name)
	}
	return v
}
