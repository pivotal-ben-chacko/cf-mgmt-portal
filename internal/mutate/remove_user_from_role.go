package mutate

import (
	"fmt"

	"gopkg.in/yaml.v2"
)

// RemoveUserFromSpaceRole reads a spaceConfig.yml, removes username from the
// user list for the given role+origin, and returns the new YAML bytes. The edit
// is done at the node level so the diff contains only the removed line (see
// nodeedit.go). The input bytes are not modified. If the user is not present,
// the original bytes are returned unchanged.
func RemoveUserFromSpaceRole(current []byte, role Role, origin Origin, username string) ([]byte, error) {
	var doc yaml.MapSlice
	if err := yaml.Unmarshal(current, &doc); err != nil {
		return nil, fmt.Errorf("parse spaceConfig.yml: %w", err)
	}
	doc, changed, err := applyUserEdit(doc, role, origin, username, "remove")
	if err != nil {
		return nil, err
	}
	if !changed {
		return current, nil
	}
	return yaml.Marshal(doc)
}
