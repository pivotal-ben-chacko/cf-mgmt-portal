package mutate

import (
	"fmt"

	"gopkg.in/yaml.v2"
)

// This file edits spaceConfig.yml at the node level using yaml.v2's ordered
// MapSlice, instead of unmarshalling into the typed cf-mgmt SpaceConfig and
// re-marshalling. The typed round-trip materialises every non-omitempty field
// (e.g. it injects an empty space-supporter block into spaces that don't have
// one), which pollutes the MR diff. Node-level editing changes only the one
// list the action targets and leaves the rest of the document byte-for-byte
// intact — exactly what a review-based workflow wants.

// canonicalRoles is the order cf-mgmt writes space role blocks in; used to
// place a newly-created role block in the right spot.
var canonicalRoles = []string{"space-developer", "space-manager", "space-auditor", "space-supporter"}

func roleKey(role Role) (string, error) {
	switch role {
	case RoleDeveloper:
		return "space-developer", nil
	case RoleManager:
		return "space-manager", nil
	case RoleAuditor:
		return "space-auditor", nil
	case RoleSupporter:
		return "space-supporter", nil
	}
	return "", fmt.Errorf("unknown role: %q", role)
}

func originKey(origin Origin) (string, error) {
	switch origin {
	case OriginLDAP:
		return "ldap_users", nil
	case OriginSAML:
		return "saml_users", nil
	case OriginInternal:
		return "users", nil
	}
	return "", fmt.Errorf("unknown origin: %q", origin)
}

// roleListKeys are the member-list keys inside a space-<role> block.
var roleListKeys = []string{"ldap_users", "users", "saml_users", "ldap_groups"}

// normalizeRoleLists replaces nil member lists inside space-role blocks (a
// bare `ldap_groups:` key in the source) with empty sequences, so the
// full-document re-marshal renders them as `[]` — matching cf-mgmt's own
// output style — rather than an explicit `null`. Callers run this right after
// unmarshalling; the no-op paths still return the caller's original bytes, so
// normalization only ever reaches the output of a real change.
func normalizeRoleLists(doc yaml.MapSlice) yaml.MapSlice {
	for i, it := range doc {
		if canonicalRoleIdx(keyStr(it.Key)) < 0 {
			continue
		}
		block, ok := it.Value.(yaml.MapSlice)
		if !ok {
			continue
		}
		for j, item := range block {
			if item.Value != nil {
				continue
			}
			for _, lk := range roleListKeys {
				if keyStr(item.Key) == lk {
					block[j].Value = []interface{}{}
					break
				}
			}
		}
		doc[i].Value = block
	}
	return doc
}

// applyUserEdit adds/removes a user under space-<role> / <origin>_users.
func applyUserEdit(doc yaml.MapSlice, role Role, origin Origin, user, action string) (yaml.MapSlice, bool, error) {
	listKey, err := originKey(origin)
	if err != nil {
		return doc, false, err
	}
	return applyListEdit(doc, role, listKey, user, action)
}

// applyGroupEdit adds/removes an LDAP group under space-<role> / ldap_groups.
func applyGroupEdit(doc yaml.MapSlice, role Role, group, action string) (yaml.MapSlice, bool, error) {
	return applyListEdit(doc, role, "ldap_groups", group, action)
}

// applyListEdit adds or removes member from the listKey sequence under
// space-<role> in the parsed document (listKey is e.g. ldap_users, saml_users,
// users, or ldap_groups). It returns the (possibly reallocated) document and
// whether anything changed. On "add" a missing role block or list is created;
// on "remove" a missing role block / list / member is a no-op.
func applyListEdit(doc yaml.MapSlice, role Role, listKey, member, action string) (yaml.MapSlice, bool, error) {
	if member == "" {
		return doc, false, fmt.Errorf("empty member")
	}
	rk, err := roleKey(role)
	if err != nil {
		return doc, false, err
	}

	ri := indexOfKey(doc, rk)

	switch action {
	case "add":
		if ri < 0 {
			return insertRoleBlock(doc, rk, newRoleBlock(listKey, member)), true, nil
		}
		block := asMapSlice(doc[ri].Value)
		li := indexOfKey(block, listKey)
		if li < 0 {
			block = append(block, yaml.MapItem{Key: listKey, Value: []interface{}{member}})
			doc[ri].Value = block
			return doc, true, nil
		}
		seq := asSeq(block[li].Value)
		if seqContains(seq, member) {
			return doc, false, nil
		}
		block[li].Value = append(seq, member)
		doc[ri].Value = block
		return doc, true, nil

	case "remove":
		if ri < 0 {
			return doc, false, nil
		}
		block := asMapSlice(doc[ri].Value)
		li := indexOfKey(block, listKey)
		if li < 0 {
			return doc, false, nil
		}
		seq := asSeq(block[li].Value)
		if !seqContains(seq, member) {
			return doc, false, nil
		}
		block[li].Value = seqRemove(seq, member)
		doc[ri].Value = block
		return doc, true, nil
	}
	return doc, false, fmt.Errorf("unknown action: %q", action)
}

// newRoleBlock builds a fresh UserMgmt block in cf-mgmt's canonical key order
// with member placed in the listKey list and the other lists empty.
func newRoleBlock(listKey, member string) yaml.MapSlice {
	block := yaml.MapSlice{
		{Key: "ldap_users", Value: []interface{}{}},
		{Key: "users", Value: []interface{}{}},
		{Key: "saml_users", Value: []interface{}{}},
		{Key: "ldap_groups", Value: []interface{}{}},
	}
	for i := range block {
		if block[i].Key == listKey {
			block[i].Value = []interface{}{member}
		}
	}
	return block
}

// insertRoleBlock inserts a new space-<role> block keeping canonical role order:
// before the first role with a higher canonical index, or before the first
// trailing config key (allow-ssh, enable-*, …), whichever comes first.
func insertRoleBlock(doc yaml.MapSlice, rk string, block yaml.MapSlice) yaml.MapSlice {
	desired := canonicalRoleIdx(rk)
	insertAt := len(doc)
	for i, it := range doc {
		k := keyStr(it.Key)
		if k == "org" || k == "space" {
			continue
		}
		if ci := canonicalRoleIdx(k); ci >= 0 {
			if ci > desired {
				insertAt = i
				break
			}
			continue
		}
		// a non-role, non-org/space key marks the start of the trailing config
		insertAt = i
		break
	}
	item := yaml.MapItem{Key: rk, Value: block}
	out := make(yaml.MapSlice, 0, len(doc)+1)
	out = append(out, doc[:insertAt]...)
	out = append(out, item)
	out = append(out, doc[insertAt:]...)
	return out
}

func canonicalRoleIdx(key string) int {
	for i, r := range canonicalRoles {
		if r == key {
			return i
		}
	}
	return -1
}

func indexOfKey(ms yaml.MapSlice, key string) int {
	for i, it := range ms {
		if keyStr(it.Key) == key {
			return i
		}
	}
	return -1
}

func keyStr(k interface{}) string {
	if s, ok := k.(string); ok {
		return s
	}
	return fmt.Sprint(k)
}

func asMapSlice(v interface{}) yaml.MapSlice {
	if ms, ok := v.(yaml.MapSlice); ok {
		return ms
	}
	return yaml.MapSlice{}
}

func asSeq(v interface{}) []interface{} {
	if s, ok := v.([]interface{}); ok {
		return s
	}
	return []interface{}{}
}

func seqContains(seq []interface{}, user string) bool {
	for _, v := range seq {
		if keyStr(v) == user {
			return true
		}
	}
	return false
}

func seqRemove(seq []interface{}, user string) []interface{} {
	out := make([]interface{}, 0, len(seq))
	for _, v := range seq {
		if keyStr(v) != user {
			out = append(out, v)
		}
	}
	return out
}
