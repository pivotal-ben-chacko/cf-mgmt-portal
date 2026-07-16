package mutate

import (
	"reflect"
	"testing"
)

func TestListOrgUsers(t *testing.T) {
	in := []byte(`
org: epay-org
org-billingmanager:
  ldap_users: []
org-manager:
  ldap_users:
  - jstlouis
  users:
  - ePaySvcAcct
org-auditor:
  saml_users:
  - aud@corp.example
`)
	got, err := ListOrgUsers(in)
	if err != nil {
		t.Fatal(err)
	}
	want := []RoleUsers{
		{Role: RoleBillingManager, LDAP: []string{}, SAML: []string{}, Internal: []string{}},
		{Role: RoleManager, LDAP: []string{"jstlouis"}, SAML: []string{}, Internal: []string{"ePaySvcAcct"}},
		{Role: RoleAuditor, LDAP: []string{}, SAML: []string{"aud@corp.example"}, Internal: []string{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListOrgUsers mismatch:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestListOrgUsers_MissingBlocksReturnEmptyLists(t *testing.T) {
	got, err := ListOrgUsers([]byte("org: bare"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 roles, got %d", len(got))
	}
	for _, r := range got {
		if len(r.LDAP)+len(r.SAML)+len(r.Internal) != 0 {
			t.Errorf("expected empty lists for role %s, got %#v", r.Role, r)
		}
	}
}

func TestListOrgUsers_BadYAMLErrors(t *testing.T) {
	if _, err := ListOrgUsers([]byte(":\n:::")); err == nil {
		t.Fatal("expected parse error")
	}
}
