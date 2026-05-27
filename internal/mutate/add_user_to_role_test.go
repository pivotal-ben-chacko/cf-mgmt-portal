package mutate

import (
	"strings"
	"testing"
)

func TestAddUserToSpaceRole_AppendsToLDAPUsers(t *testing.T) {
	in := []byte(`
org: epay-org
space: cfnapp
space-developer:
  ldap_users:
    - F920U2K
    - F9HYXEL
allow-ssh: false
`)
	out, err := AddUserToSpaceRole(in, RoleDeveloper, OriginLDAP, "F9NEWUSR")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "F9NEWUSR") {
		t.Fatalf("expected new user in output, got:\n%s", out)
	}
}

func TestAddUserToSpaceRole_IdempotentForExistingUser(t *testing.T) {
	in := []byte(`
org: epay-org
space: cfnapp
space-developer:
  ldap_users:
    - F920U2K
`)
	out, err := AddUserToSpaceRole(in, RoleDeveloper, OriginLDAP, "F920U2K")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(out), "F920U2K") != 1 {
		t.Fatalf("expected exactly one F920U2K in output, got:\n%s", out)
	}
}

func TestAddUserToSpaceRole_UnknownRoleReturnsError(t *testing.T) {
	in := []byte(`org: o
space: s`)
	if _, err := AddUserToSpaceRole(in, Role("bogus"), OriginLDAP, "x"); err == nil {
		t.Fatal("expected error for unknown role")
	}
}
