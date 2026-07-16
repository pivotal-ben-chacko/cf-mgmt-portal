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

func TestAddUserToSpaceRole_BareNilListsRenderAsEmptySeq(t *testing.T) {
	// A bare `ldap_groups:` (implicit null) must come back as `ldap_groups: []`,
	// not `ldap_groups: null`, when the document is rewritten.
	in := []byte(`org: epay-org
space: cfnapp
space-developer:
  ldap_users:
  - jahoward
  users: []
  saml_users: []
  ldap_groups:
space-manager:
  ldap_users:
  - jahoward
`)
	out, err := AddUserToSpaceRole(in, RoleDeveloper, OriginLDAP, "F7PAYU0")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "null") {
		t.Errorf("expected no explicit null in output, got:\n%s", s)
	}
	if !strings.Contains(s, "ldap_groups: []") {
		t.Errorf("expected bare ldap_groups normalized to [], got:\n%s", s)
	}
}

func TestAddUserToSpaceRole_NoOpLeavesBareNilListsUntouched(t *testing.T) {
	// The no-op path must still return the original bytes byte-for-byte, even
	// when the source contains lists that normalization would rewrite.
	in := []byte(`org: epay-org
space: cfnapp
space-developer:
  ldap_users:
  - F920U2K
  ldap_groups:
`)
	out, err := AddUserToSpaceRole(in, RoleDeveloper, OriginLDAP, "F920U2K")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Fatalf("expected original bytes for no-op, got:\n%s", out)
	}
}

func TestAddUserToSpaceRole_UnknownRoleReturnsError(t *testing.T) {
	in := []byte(`org: o
space: s`)
	if _, err := AddUserToSpaceRole(in, Role("bogus"), OriginLDAP, "x"); err == nil {
		t.Fatal("expected error for unknown role")
	}
}
