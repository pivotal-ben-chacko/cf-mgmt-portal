package mutate

import (
	"fmt"

	"github.com/vmwarepivotallabs/cf-mgmt/config"
	"gopkg.in/yaml.v2"
)

// RoleGroups is the set of LDAP groups bound to a single role in a space.
// Groups in cf-mgmt are LDAP-only (the ldap_groups list inside each space-<role>
// block), so there is no origin dimension as there is for users.
type RoleGroups struct {
	Role   Role     `json:"role"`
	Groups []string `json:"groups"`
}

// ListSpaceGroups parses a spaceConfig.yml and returns the ldap_groups bound to
// each role, in the fixed order developer, manager, auditor, supporter. It is a
// pure read; nil slices render as empty JSON arrays via the handler.
func ListSpaceGroups(spaceConfig []byte) ([]RoleGroups, error) {
	var cfg config.SpaceConfig
	if err := yaml.Unmarshal(spaceConfig, &cfg); err != nil {
		return nil, fmt.Errorf("parse spaceConfig.yml: %w", err)
	}

	roles := []Role{RoleDeveloper, RoleManager, RoleAuditor, RoleSupporter}
	out := make([]RoleGroups, 0, len(roles))
	for _, role := range roles {
		mgmt, err := roleField(&cfg, role)
		if err != nil {
			return nil, err
		}
		out = append(out, RoleGroups{Role: role, Groups: nonNil(mgmt.LDAPGroups)})
	}
	return out, nil
}
