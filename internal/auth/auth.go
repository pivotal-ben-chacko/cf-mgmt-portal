// Package auth handles user authentication via GitLab OAuth.
//
// We use GitLab (not the foundation's UAA) as the identity provider because:
//   - Users already have GitLab accounts to open MRs.
//   - GitLab is LDAP-synced, so GitLab username == sAMAccountName (e.g. F920U2K)
//     which matches the user identity used in spaceConfig.yml.
//   - Single OAuth client, regardless of how many CF foundations we manage.
//
// CF API authorization checks (does this user hold OrgManager on org X?) use a
// separate service-account UAA client; see internal/cfapi.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

type Client struct {
	cfg       *oauth2.Config
	gitlabURL string
	http      *http.Client
}

type User struct {
	GitLabID int    // numeric GitLab user ID
	Username string // matches sAMAccountName / LDAP id, e.g. F920U2K
	Email    string
}

func NewGitLabOAuth(gitlabURL, clientID, clientSecret, redirectURL string) (*Client, error) {
	gitlabURL = strings.TrimRight(gitlabURL, "/")
	return &Client{
		gitlabURL: gitlabURL,
		http:      &http.Client{Timeout: 15 * time.Second},
		cfg: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"read_user"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  gitlabURL + "/oauth/authorize",
				TokenURL: gitlabURL + "/oauth/token",
			},
		},
	}, nil
}

// AuthURL builds the GitLab /oauth/authorize redirect target. `state` must be a
// cryptographically random per-request value the caller persists in a cookie
// and verifies on callback.
func (c *Client) AuthURL(state string) string {
	return c.cfg.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

// Exchange swaps an authorization code for an access token, then looks up the
// authenticated user via GET /api/v4/user.
func (c *Client) Exchange(ctx context.Context, code string) (User, error) {
	tok, err := c.cfg.Exchange(ctx, code)
	if err != nil {
		return User{}, fmt.Errorf("oauth exchange: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.gitlabURL+"/api/v4/user", nil)
	if err != nil {
		return User{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return User{}, fmt.Errorf("gitlab user lookup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return User{}, fmt.Errorf("gitlab user lookup: status %d", resp.StatusCode)
	}

	var raw struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
		Email    string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return User{}, fmt.Errorf("decode user: %w", err)
	}
	return User{GitLabID: raw.ID, Username: raw.Username, Email: raw.Email}, nil
}
