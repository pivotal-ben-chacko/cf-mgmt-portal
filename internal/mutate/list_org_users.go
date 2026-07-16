package mutate

import (
	"fmt"

	"github.com/vmwarepivotallabs/cf-mgmt/config"
	"gopkg.in/yaml.v2"
)

// ListOrgUsers parses an orgConfig.yml and returns the users in each org role,
// in the fixed order billingmanager, manager, auditor (the order cf-mgmt writes
// the blocks in). It is a pure read (no I/O); nil slices render as empty JSON
// arrays via the handler.
func ListOrgUsers(orgConfig []byte) ([]RoleUsers, error) {
	var cfg config.OrgConfig
	if err := yaml.Unmarshal(orgConfig, &cfg); err != nil {
		return nil, fmt.Errorf("parse orgConfig.yml: %w", err)
	}

	roles := []Role{RoleBillingManager, RoleManager, RoleAuditor}
	out := make([]RoleUsers, 0, len(roles))
	for _, role := range roles {
		mgmt, err := orgRoleField(&cfg, role)
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

func orgRoleField(o *config.OrgConfig, role Role) (*config.UserMgmt, error) {
	switch role {
	case RoleBillingManager:
		return &o.BillingManager, nil
	case RoleManager:
		return &o.Manager, nil
	case RoleAuditor:
		return &o.Auditor, nil
	}
	return nil, fmt.Errorf("unknown org role: %q", role)
}
