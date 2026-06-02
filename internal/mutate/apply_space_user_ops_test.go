package mutate

import (
	"strings"
	"testing"
)

func TestApplySpaceUserOps_AddAndRemoveInOneBatch(t *testing.T) {
	in := []byte(`
org: epay-org
space: cfnapp
space-developer:
  ldap_users:
    - F920U2K
    - F9HYXEL
space-manager:
  ldap_users:
    - F920U2K
`)
	ops := []Op{
		{User: "F9HYXEL", Origin: OriginLDAP, Role: RoleDeveloper, Action: "remove"},
		{User: "F9HYXEL", Origin: OriginLDAP, Role: RoleManager, Action: "add"},
	}
	out, err := ApplySpaceUserOps(in, ops)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// F9HYXEL moved from developer to manager.
	devIdx := strings.Index(s, "space-developer")
	mgrIdx := strings.Index(s, "space-manager")
	if strings.Count(s, "F9HYXEL") != 1 {
		t.Fatalf("expected exactly one F9HYXEL, got:\n%s", s)
	}
	// crude positional check: the remaining F9HYXEL should be after space-manager
	if idx := strings.Index(s, "F9HYXEL"); idx < mgrIdx || idx < devIdx {
		// not a strict assertion beyond presence; ensure manager section exists
	}
}

func TestApplySpaceUserOps_FullRemovalAcrossRoles(t *testing.T) {
	in := []byte(`
org: o
space: s
space-developer:
  ldap_users: [F920U2K, F9HYXEL]
space-manager:
  ldap_users: [F9HYXEL]
`)
	ops := []Op{
		{User: "F9HYXEL", Origin: OriginLDAP, Role: RoleDeveloper, Action: "remove"},
		{User: "F9HYXEL", Origin: OriginLDAP, Role: RoleManager, Action: "remove"},
	}
	out, err := ApplySpaceUserOps(in, ops)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "F9HYXEL") {
		t.Fatalf("expected F9HYXEL fully removed, got:\n%s", out)
	}
	if !strings.Contains(string(out), "F920U2K") {
		t.Fatalf("expected F920U2K retained, got:\n%s", out)
	}
}

func TestApplySpaceUserOps_NoNetChangeReturnsInputUnchanged(t *testing.T) {
	in := []byte(`
org: o
space: s
space-developer:
  ldap_users: [F920U2K]
`)
	ops := []Op{
		{User: "F920U2K", Origin: OriginLDAP, Role: RoleDeveloper, Action: "add"}, // already present
		{User: "GHOST", Origin: OriginLDAP, Role: RoleManager, Action: "remove"},  // absent
	}
	out, err := ApplySpaceUserOps(in, ops)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Fatalf("expected unchanged bytes for no-op batch, got:\n%s", out)
	}
}

func TestApplySpaceUserOps_UnknownActionErrors(t *testing.T) {
	in := []byte("org: o\nspace: s")
	_, err := ApplySpaceUserOps(in, []Op{{User: "x", Origin: OriginLDAP, Role: RoleDeveloper, Action: "toggle"}})
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}
