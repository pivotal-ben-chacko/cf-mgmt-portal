package mutate

import "testing"

func TestListSpaceUsers_GroupsByRoleAndOrigin(t *testing.T) {
	in := []byte(`
org: epay-org
space: cfnapp
space-developer:
  ldap_users:
    - F920U2K
    - F9HYXEL
space-manager:
  saml_users:
    - alice@example.com
`)
	got, err := ListSpaceUsers(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 roles, got %d", len(got))
	}
	if got[0].Role != RoleDeveloper || len(got[0].LDAP) != 2 {
		t.Fatalf("developer ldap users wrong: %+v", got[0])
	}
	if got[1].Role != RoleManager || len(got[1].SAML) != 1 || got[1].SAML[0] != "alice@example.com" {
		t.Fatalf("manager saml users wrong: %+v", got[1])
	}
	// Empty role lists must be non-nil so they marshal as [] not null.
	if got[2].LDAP == nil || got[2].SAML == nil || got[2].Internal == nil {
		t.Fatalf("expected empty (non-nil) slices for auditor, got %+v", got[2])
	}
}

func TestListSpaceUsers_BadYAMLErrors(t *testing.T) {
	if _, err := ListSpaceUsers([]byte("\t not: yaml: [")); err == nil {
		t.Fatal("expected error for malformed yaml")
	}
}
