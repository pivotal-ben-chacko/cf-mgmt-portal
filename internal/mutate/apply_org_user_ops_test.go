package mutate

import (
	"strings"
	"testing"
)

// prodStyleOrgConfig mirrors the shape cf-mgmt writes for an orgConfig.yml:
// all three role blocks present, trailing quota/domain config after them.
const prodStyleOrgConfig = `org: epay-org
org-billingmanager:
  ldap_users: []
  users: []
  saml_users: []
  ldap_groups: []
org-manager:
  ldap_users:
  - jstlouis
  users: []
  saml_users: []
  ldap_groups: []
org-auditor:
  ldap_users:
  - rskumar
  - vchouhan
  users: []
  saml_users: []
  ldap_groups: []
private-domains: []
enable-remove-private-domains: false
shared-private-domains: []
enable-remove-shared-private-domains: false
enable-org-quota: false
memory-limit: unlimited
paid-service-plans-allowed: false
enable-remove-users: false
default_isolation_segment: ""
named_quota: ""
metadata: null
`

func TestApplyOrgUserOps_AddManager_MinimalDiff(t *testing.T) {
	ops := []Op{{User: "F920U2K", Origin: OriginLDAP, Role: RoleManager, Action: "add"}}
	out, err := ApplyOrgUserOps([]byte(prodStyleOrgConfig), ops)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "- F920U2K") {
		t.Errorf("expected new manager in output:\n%s", s)
	}
	for _, sentinel := range []string{
		"memory-limit: unlimited",
		`default_isolation_segment: ""`,
		"metadata: null",
		"- jstlouis",
	} {
		if !strings.Contains(s, sentinel) {
			t.Errorf("expected preserved field %q missing from output:\n%s", sentinel, s)
		}
	}
	if got, want := strings.Count(s, "\n"), strings.Count(prodStyleOrgConfig, "\n")+1; got != want {
		t.Errorf("expected exactly 1 line added: got %d lines, want %d", got, want)
	}
}

func TestApplyOrgUserOps_MoveUserBetweenRoles(t *testing.T) {
	ops := []Op{
		{User: "rskumar", Origin: OriginLDAP, Role: RoleAuditor, Action: "remove"},
		{User: "rskumar", Origin: OriginLDAP, Role: RoleBillingManager, Action: "add"},
	}
	out, err := ApplyOrgUserOps([]byte(prodStyleOrgConfig), ops)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Count(s, "rskumar") != 1 {
		t.Fatalf("expected exactly one rskumar, got:\n%s", s)
	}
	bmIdx := strings.Index(s, "org-billingmanager")
	mgrIdx := strings.Index(s, "org-manager")
	userIdx := strings.Index(s, "rskumar")
	if !(bmIdx < userIdx && userIdx < mgrIdx) {
		t.Errorf("expected rskumar inside org-billingmanager block:\n%s", s)
	}
}

func TestApplyOrgUserOps_MissingBlockCreatedInOrder(t *testing.T) {
	in := []byte(`org: o
org-manager:
  ldap_users:
  - boss
enable-org-quota: false
`)
	ops := []Op{{User: "aud", Origin: OriginLDAP, Role: RoleAuditor, Action: "add"}}
	out, err := ApplyOrgUserOps(in, ops)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	mgrIdx := strings.Index(s, "org-manager")
	audIdx := strings.Index(s, "org-auditor")
	quotaIdx := strings.Index(s, "enable-org-quota")
	if audIdx < 0 || !(mgrIdx < audIdx && audIdx < quotaIdx) {
		t.Errorf("org-auditor not placed between org-manager and trailing config:\n%s", s)
	}
	if !strings.Contains(s, "- aud") {
		t.Errorf("expected aud in new auditor block:\n%s", s)
	}
}

func TestApplyOrgUserOps_NoNetChangeReturnsInputUnchanged(t *testing.T) {
	ops := []Op{
		{User: "jstlouis", Origin: OriginLDAP, Role: RoleManager, Action: "add"}, // already present
		{User: "ghost", Origin: OriginLDAP, Role: RoleAuditor, Action: "remove"}, // absent
	}
	out, err := ApplyOrgUserOps([]byte(prodStyleOrgConfig), ops)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != prodStyleOrgConfig {
		t.Errorf("expected original bytes returned unchanged for net no-op batch")
	}
}

func TestApplyOrgUserOps_SpaceRoleRejected(t *testing.T) {
	in := []byte("org: o")
	_, err := ApplyOrgUserOps(in, []Op{{User: "x", Origin: OriginLDAP, Role: RoleDeveloper, Action: "add"}})
	if err == nil {
		t.Fatal("expected error for space-only role in org scope")
	}
}

func TestApplyOrgUserOps_UnknownActionErrors(t *testing.T) {
	in := []byte("org: o")
	_, err := ApplyOrgUserOps(in, []Op{{User: "x", Origin: OriginLDAP, Role: RoleManager, Action: "toggle"}})
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestApplyOrgUserOps_NilRoleListRendersEmptySeq(t *testing.T) {
	in := []byte(`org: o
org-manager:
  ldap_users:
  - boss
  ldap_groups:
`)
	ops := []Op{{User: "next", Origin: OriginLDAP, Role: RoleManager, Action: "add"}}
	out, err := ApplyOrgUserOps(in, ops)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "ldap_groups: null") {
		t.Errorf("expected bare ldap_groups normalized to [], got:\n%s", out)
	}
	if !strings.Contains(string(out), "ldap_groups: []") {
		t.Errorf("expected ldap_groups rendered as [], got:\n%s", out)
	}
}
