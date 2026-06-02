package mutate

import (
	"fmt"

	"github.com/vmwarepivotallabs/cf-mgmt/config"
	"gopkg.in/yaml.v2"
)

// RoleUsers is the set of users holding a single role in a space, grouped by
// origin. JSON tags match the form field values used elsewhere (ldap/saml/internal).
type RoleUsers struct {
	Role     Role     `json:"role"`
	LDAP     []string `json:"ldap"`
	SAML     []string `json:"saml"`
	Internal []string `json:"internal"`
}

// ListSpaceUsers parses a spaceConfig.yml and returns the users in each role,
// in the fixed order developer, manager, auditor, supporter. It is a pure read
// (no I/O); nil slices render as empty JSON arrays via the handler.
func ListSpaceUsers(spaceConfig []byte) ([]RoleUsers, error) {
	var cfg config.SpaceConfig
	if err := yaml.Unmarshal(spaceConfig, &cfg); err != nil {
		return nil, fmt.Errorf("parse spaceConfig.yml: %w", err)
	}

	roles := []Role{RoleDeveloper, RoleManager, RoleAuditor, RoleSupporter}
	out := make([]RoleUsers, 0, len(roles))
	for _, role := range roles {
		mgmt, err := roleField(&cfg, role)
		if err != nil {
			return nil, err
		}
		out = append(out, RoleUsers{
			Role:     role,
			LDAP:     nonNil(mgmt.LDAPUsers),
			SAML:     nonNil(mgmt.SamlUsers),
			Internal: nonNil(mgmt.Users),
		})
	}
	return out, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
