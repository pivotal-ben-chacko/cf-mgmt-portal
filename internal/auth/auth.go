// Package auth handles user authentication via the foundation's UAA
// (authorization-code OAuth with the `openid` scope).
//
// We use UAA (not GitLab) as the identity provider because:
//   - UAA is LDAP-integrated, and for LDAP-origin users the `user_name` claim
//     from /userinfo IS the sAMAccountName (e.g. F920U2K) — the exact identity
//     used in spaceConfig.yml and by the CF role check. GitLab usernames turned
//     out not to match sAMAccountName on the production instance.
//   - Every portal user is a CF org manager, so they exist in UAA by
//     definition; a GitLab account is not guaranteed.
//   - Users enter their AD credentials on UAA's own login page (same trust as
//     `cf login`); the portal never sees a password.
//
// This login client (authorization_code, scope openid, no CF API authorities)
// is separate from the read-only service-account client used for CF API
// authorization checks; see internal/cfapi and docs/cf-uaa-setup.md.
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
	cfg    *oauth2.Config
	uaaURL string
	http   *http.Client
}

type User struct {
	Username string // UAA user_name claim == sAMAccountName / LDAP id, e.g. F920U2K
	Email    string
}

func NewUAAOAuth(uaaURL, clientID, clientSecret, redirectURL string) (*Client, error) {
	uaaURL = strings.TrimRight(uaaURL, "/")
	return &Client{
		uaaURL: uaaURL,
		http:   &http.Client{Timeout: 15 * time.Second},
		cfg: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"openid"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  uaaURL + "/oauth/authorize",
				TokenURL: uaaURL + "/oauth/token",
			},
		},
	}, nil
}

// AuthURL builds the UAA /oauth/authorize redirect target. `state` must be a
// cryptographically random per-request value the caller persists in a cookie
// and verifies on callback.
func (c *Client) AuthURL(state string) string {
	return c.cfg.AuthCodeURL(state, oauth2.AccessTypeOnline)
}

// Exchange swaps an authorization code for an access token, then looks up the
// authenticated user via GET /userinfo.
func (c *Client) Exchange(ctx context.Context, code string) (User, error) {
	tok, err := c.cfg.Exchange(ctx, code)
	if err != nil {
		return User{}, fmt.Errorf("oauth exchange: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.uaaURL+"/userinfo", nil)
	if err != nil {
		return User{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return User{}, fmt.Errorf("uaa userinfo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return User{}, fmt.Errorf("uaa userinfo: status %d", resp.StatusCode)
	}

	var raw struct {
		Username string `json:"user_name"`
		Email    string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return User{}, fmt.Errorf("decode userinfo: %w", err)
	}
	if raw.Username == "" {
		return User{}, fmt.Errorf("uaa userinfo: empty user_name claim")
	}
	return User{Username: raw.Username, Email: raw.Email}, nil
}
