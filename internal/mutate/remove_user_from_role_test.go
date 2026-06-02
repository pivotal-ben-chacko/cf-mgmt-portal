package mutate

import (
	"strings"
	"testing"
)

func TestRemoveUserFromSpaceRole_RemovesFromLDAPUsers(t *testing.T) {
	in := []byte(`
org: epay-org
space: cfnapp
space-developer:
  ldap_users:
    - F920U2K
    - F9HYXEL
allow-ssh: false
`)
	out, err := RemoveUserFromSpaceRole(in, RoleDeveloper, OriginLDAP, "F9HYXEL")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "F9HYXEL") {
		t.Fatalf("expected user removed from output, got:\n%s", out)
	}
	if !strings.Contains(string(out), "F920U2K") {
		t.Fatalf("expected other user retained, got:\n%s", out)
	}
}

func TestRemoveUserFromSpaceRole_NoopForAbsentUser(t *testing.T) {
	in := []byte(`
org: epay-org
space: cfnapp
space-developer:
  ldap_users:
    - F920U2K
`)
	out, err := RemoveUserFromSpaceRole(in, RoleDeveloper, OriginLDAP, "F9NOTHERE")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Fatalf("expected unchanged bytes for absent user, got:\n%s", out)
	}
}

func TestRemoveUserFromSpaceRole_UnknownRoleReturnsError(t *testing.T) {
	in := []byte(`org: o
space: s`)
	if _, err := RemoveUserFromSpaceRole(in, Role("bogus"), OriginLDAP, "x"); err == nil {
		t.Fatal("expected error for unknown role")
	}
}
