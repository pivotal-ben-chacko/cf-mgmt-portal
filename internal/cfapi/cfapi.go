// Package cfapi is a minimal CF API v3 (and UAA SCIM read) client used to
// authorize the requesting user (whose username matches their LDAP/CF
// identity) before the portal will mutate an org's YAML: either they hold the
// appropriate role on the target org, or they belong to a configured admin
// UAA group.
//
// The portal NEVER writes to CF or UAA via this client — all CF mutations flow
// through the cf-mgmt pipeline. Read-only is sufficient.
package cfapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2/clientcredentials"
)

// Role enumerates CF v3 org-level role types.
type Role string

const (
	RoleOrgManager        Role = "organization_manager"
	RoleOrgAuditor        Role = "organization_auditor"
	RoleOrgBillingManager Role = "organization_billing_manager"
)

type Client struct {
	apiURL string
	uaaURL string
	http   *http.Client
}

func NewClient(apiURL, uaaURL, uaaClientID, uaaClientSecret string) *Client {
	uaaURL = strings.TrimRight(uaaURL, "/")
	cfg := &clientcredentials.Config{
		ClientID:     uaaClientID,
		ClientSecret: uaaClientSecret,
		TokenURL:     uaaURL + "/oauth/token",
	}
	httpClient := cfg.Client(context.Background())
	httpClient.Timeout = 30 * time.Second
	return &Client{
		apiURL: strings.TrimRight(apiURL, "/"),
		uaaURL: uaaURL,
		http:   httpClient,
	}
}

// UserHasOrgRole returns true iff `ldapUsername` (e.g. F920U2K) holds exactly
// `role` on the given org. Returns false (no error) if the user does not exist
// in CF, since they obviously can't hold any role. Returns an error if the org
// does not exist.
func (c *Client) UserHasOrgRole(ctx context.Context, ldapUsername, orgName string, role Role) (bool, error) {
	orgGUID, err := c.orgGUID(ctx, orgName)
	if err != nil {
		return false, fmt.Errorf("lookup org %q: %w", orgName, err)
	}
	if orgGUID == "" {
		return false, fmt.Errorf("org %q not found", orgName)
	}
	userGUID, err := c.userGUID(ctx, ldapUsername)
	if err != nil {
		return false, fmt.Errorf("lookup user %q: %w", ldapUsername, err)
	}
	if userGUID == "" {
		return false, nil
	}
	return c.hasRole(ctx, userGUID, orgGUID, role)
}

// UserGroups returns the UAA group names (SCIM groups[].display) of every UAA
// identity with the given username. Local dev stacks can hold two identities
// per username (a uaa-origin login user and a shadow ldap-origin user), so the
// result is the deduped union across origins. Requires the service-account
// client to hold the `scim.read` authority. An unknown username is not an
// error; it returns an empty list.
func (c *Client) UserGroups(ctx context.Context, username string) ([]string, error) {
	filter := fmt.Sprintf("userName eq %q", username)
	u := c.uaaURL + "/Users?attributes=userName,groups&filter=" + url.QueryEscape(filter)
	var resp struct {
		Resources []struct {
			Groups []struct {
				Display string `json:"display"`
			} `json:"groups"`
		} `json:"resources"`
	}
	if err := c.get(ctx, u, &resp); err != nil {
		return nil, fmt.Errorf("uaa lookup user %q: %w", username, err)
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range resp.Resources {
		for _, g := range r.Groups {
			if g.Display == "" || seen[g.Display] {
				continue
			}
			seen[g.Display] = true
			out = append(out, g.Display)
		}
	}
	return out, nil
}

func (c *Client) orgGUID(ctx context.Context, name string) (string, error) {
	var resp resources
	u := c.apiURL + "/v3/organizations?names=" + url.QueryEscape(name)
	if err := c.get(ctx, u, &resp); err != nil {
		return "", err
	}
	if len(resp.Resources) == 0 {
		return "", nil
	}
	return resp.Resources[0].GUID, nil
}

func (c *Client) userGUID(ctx context.Context, ldapUsername string) (string, error) {
	var resp resources
	u := c.apiURL + "/v3/users?usernames=" + url.QueryEscape(ldapUsername) + "&origins=ldap"
	if err := c.get(ctx, u, &resp); err != nil {
		return "", err
	}
	if len(resp.Resources) == 0 {
		return "", nil
	}
	return resp.Resources[0].GUID, nil
}

func (c *Client) hasRole(ctx context.Context, userGUID, orgGUID string, role Role) (bool, error) {
	var resp resources
	u := fmt.Sprintf("%s/v3/roles?organization_guids=%s&user_guids=%s&types=%s",
		c.apiURL,
		url.QueryEscape(orgGUID),
		url.QueryEscape(userGUID),
		url.QueryEscape(string(role)),
	)
	if err := c.get(ctx, u, &resp); err != nil {
		return false, err
	}
	return len(resp.Resources) > 0, nil
}

func (c *Client) get(ctx context.Context, urlStr string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("cf api %s: status %d: %s", req.URL.Path, resp.StatusCode, body)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type resources struct {
	Resources []struct {
		GUID string `json:"guid"`
	} `json:"resources"`
}
