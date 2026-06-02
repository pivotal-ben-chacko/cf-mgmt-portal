package mutate

import (
	"fmt"

	"gopkg.in/yaml.v2"
)

// Op is a single add/remove of a user under one role+origin in a space.
type Op struct {
	User   string `json:"user"`
	Origin Origin `json:"origin"`
	Role   Role   `json:"role"`
	Action string `json:"action"` // "add" | "remove"
}

// ApplySpaceUserOps applies a batch of role assignments/unassignments to a
// spaceConfig.yml and returns the new YAML bytes. Ops are applied in order;
// adds are deduped and removes of an absent user are ignored, so the call is
// idempotent. Editing is done at the node level (see nodeedit.go) so the diff
// contains only the lines the ops actually change. The input bytes are not
// modified. If the batch produces no net change, the original bytes are
// returned unchanged (the no-op signal the handlers rely on).
func ApplySpaceUserOps(current []byte, ops []Op) ([]byte, error) {
	var doc yaml.MapSlice
	if err := yaml.Unmarshal(current, &doc); err != nil {
		return nil, fmt.Errorf("parse spaceConfig.yml: %w", err)
	}

	// Clean round-trip of the unedited document, to compare against after the
	// edits — this makes a net no-op batch (e.g. add then remove the same user)
	// return the original bytes.
	before, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}

	for _, op := range ops {
		doc, _, err = applyUserEdit(doc, op.Role, op.Origin, op.User, op.Action)
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
