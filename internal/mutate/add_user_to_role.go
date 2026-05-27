package mutate

import (
	"fmt"

	"github.com/vmwarepivotallabs/cf-mgmt/config"
	"gopkg.in/yaml.v2"
)

type Role string

const (
	RoleDeveloper Role = "developer"
	RoleManager   Role = "manager"
	RoleAuditor   Role = "auditor"
	RoleSupporter Role = "supporter"
)

type Origin string

const (
	OriginLDAP     Origin = "ldap"
	OriginSAML     Origin = "saml"
	OriginInternal Origin = "internal"
)

// AddUserToSpaceRole reads a spaceConfig.yml, appends username to the user list
// for the given role+origin (deduped), and returns the new YAML bytes. The
// input bytes are not modified. If the user is already present, the original
// bytes are returned unchanged.
func AddUserToSpaceRole(current []byte, role Role, origin Origin, username string) ([]byte, error) {
	var cfg config.SpaceConfig
	if err := yaml.Unmarshal(current, &cfg); err != nil {
		return nil, fmt.Errorf("parse spaceConfig.yml: %w", err)
	}

	mgmt, err := roleField(&cfg, role)
	if err != nil {
		return nil, err
	}
	list, err := originField(mgmt, origin)
	if err != nil {
		return nil, err
	}

	if contains(*list, username) {
		return current, nil
	}
	*list = append(*list, username)

	return yaml.Marshal(&cfg)
}

func roleField(s *config.SpaceConfig, role Role) (*config.UserMgmt, error) {
	switch role {
	case RoleDeveloper:
		return &s.Developer, nil
	case RoleManager:
		return &s.Manager, nil
	case RoleAuditor:
		return &s.Auditor, nil
	case RoleSupporter:
		return &s.Supporter, nil
	}
	return nil, fmt.Errorf("unknown role: %q", role)
}

func originField(u *config.UserMgmt, origin Origin) (*[]string, error) {
	switch origin {
	case OriginLDAP:
		return &u.LDAPUsers, nil
	case OriginSAML:
		return &u.SamlUsers, nil
	case OriginInternal:
		return &u.Users, nil
	}
	return nil, fmt.Errorf("unknown origin: %q", origin)
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
