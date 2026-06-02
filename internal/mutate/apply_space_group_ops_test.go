package mutate

import (
	"strings"
	"testing"
)

func TestApplySpaceGroupOps_AddAndRemove(t *testing.T) {
	in := []byte(`org: o
space: s
space-developer:
  ldap_users: []
  users: []
  saml_users: []
  ldap_groups:
  - dev-team
space-manager:
  ldap_users: []
  users: []
  saml_users: []
  ldap_groups: []
`)
	out, err := ApplySpaceGroupOps(in, []GroupOp{
		{Group: "dev-team", Role: RoleDeveloper, Action: "remove"},
		{Group: "platform-admins", Role: RoleManager, Action: "add"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "dev-team") {
		t.Errorf("expected dev-team removed, got:\n%s", s)
	}
	if !strings.Contains(s, "platform-admins") {
		t.Errorf("expected platform-admins added, got:\n%s", s)
	}
}

func TestApplySpaceGroupOps_CreatesRoleBlockForNewGroup(t *testing.T) {
	in := []byte(`org: o
space: s
space-developer:
  ldap_users: []
  users: []
  saml_users: []
  ldap_groups: []
allow-ssh: true
`)
	out, err := ApplySpaceGroupOps(in, []GroupOp{
		{Group: "aud-team", Role: RoleAuditor, Action: "add"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "space-auditor") || !strings.Contains(s, "aud-team") {
		t.Errorf("expected new space-auditor block with aud-team, got:\n%s", s)
	}
	// new block must sit before the trailing allow-ssh key
	if strings.Index(s, "space-auditor") > strings.Index(s, "allow-ssh") {
		t.Errorf("space-auditor block misplaced relative to allow-ssh:\n%s", s)
	}
}

func TestApplySpaceGroupOps_NetNoOpReturnsInput(t *testing.T) {
	in := []byte(`org: o
space: s
space-developer:
  ldap_users: []
  users: []
  saml_users: []
  ldap_groups:
  - dev-team
`)
	out, err := ApplySpaceGroupOps(in, []GroupOp{
		{Group: "dev-team", Role: RoleDeveloper, Action: "add"},     // already present
		{Group: "ghost", Role: RoleManager, Action: "remove"},       // absent
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(in) {
		t.Errorf("expected unchanged bytes for net no-op, got:\n%s", out)
	}
}

func TestListSpaceGroups_ReturnsPerRole(t *testing.T) {
	in := []byte(`org: o
space: s
space-developer:
  ldap_users: []
  users: []
  saml_users: []
  ldap_groups:
  - dev-team
  - dev-leads
space-manager:
  ldap_users: []
  users: []
  saml_users: []
  ldap_groups: []
`)
	got, err := ListSpaceGroups(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 roles, got %d", len(got))
	}
	if got[0].Role != RoleDeveloper || len(got[0].Groups) != 2 {
		t.Errorf("developer groups wrong: %+v", got[0])
	}
	if got[2].Groups == nil {
		t.Errorf("expected non-nil empty slice for auditor groups")
	}
}
