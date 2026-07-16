package mutate

import (
	"fmt"

	"gopkg.in/yaml.v2"
)

// GroupOp is a single add/remove of an LDAP group under one role in a space.
type GroupOp struct {
	Group  string `json:"group"`
	Role   Role   `json:"role"`
	Action string `json:"action"` // "add" | "remove"
}

// ApplySpaceGroupOps applies a batch of LDAP-group role bindings/unbindings to a
// spaceConfig.yml and returns the new YAML bytes. Ops are applied in order;
// adds are deduped and removes of an absent group are ignored, so the call is
// idempotent. Editing is done at the node level (see nodeedit.go) so the diff
// contains only the ldap_groups lines the ops change. The input bytes are not
// modified. If the batch produces no net change, the original bytes are
// returned unchanged.
func ApplySpaceGroupOps(current []byte, ops []GroupOp) ([]byte, error) {
	var doc yaml.MapSlice
	if err := yaml.Unmarshal(current, &doc); err != nil {
		return nil, fmt.Errorf("parse spaceConfig.yml: %w", err)
	}
	doc = normalizeRoleLists(doc)

	before, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}

	for _, op := range ops {
		doc, _, err = applyGroupEdit(doc, op.Role, op.Group, op.Action)
		if err != nil {
			return nil, err
		}
	}

	after, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if string(after) == string(before) {
		return current, nil
	}
	return after, nil
}
