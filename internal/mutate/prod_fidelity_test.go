package mutate

import (
	"strings"
	"testing"
)

// prodEpaySpaceConfig is a real production spaceConfig.yml (epay-org/dev-ips).
// It notably has NO space-supporter block, exercises mixed origins (ldap +
// internal "users"), and uses "unlimited"/null/empty-list values — a good
// fidelity fixture for the node-level editing in nodeedit.go.
const prodEpaySpaceConfig = `org: epay-org
space: dev-ips
space-developer:
  ldap_users:
  - schavan
  - ygunawan
  - kzhao
  - rtummala
  - rskumar
  - jstlouis
  - vchouhan
  - bili
  - mhaar
  - dosborne
  - rmajety
  - hyerneni
  - twjohnson
  - mgreenwood
  - srpingle
  - shujain
  - abadjate
  - wezhang
  - rarangasamy
  - jlindholm
  - jvaidyan
  - FCTHCPG
  users:
  - ePayOctoSvcFOG
  saml_users: []
  ldap_groups: []
space-manager:
  ldap_users:
  - jstlouis
  users: []
  saml_users: []
  ldap_groups: []
space-auditor:
  ldap_users:
  - rskumar
  - rarangasamy
  - vchouhan
  - rathechinamurthy
  users: []
  saml_users: []
  ldap_groups: []
allow-ssh: true
enable-space-quota: false
enable-security-group: false
enable-unassign-security-group: true
enable-remove-users: false
isolation_segment: ""
named-security-groups: []
memory-limit: unlimited
instance-memory-limit: unlimited
total-routes: unlimited
total-services: unlimited
paid-service-plans-allowed: false
total_reserved_route_ports: unlimited
total_service_keys: unlimited
app_instance_limit: unlimited
app_task_limit: unlimited
named_quota: ""
metadata: null
`

func lineCount(s string) int { return strings.Count(s, "\n") }

// assertFidelity checks that everything except the intended user lines is
// preserved, and that no empty space-supporter block was injected.
func assertFidelity(t *testing.T, in, out string) {
	t.Helper()
	if strings.Contains(out, "space-supporter") {
		t.Errorf("output injected a space-supporter block that the source lacked:\n%s", out)
	}
	for _, sentinel := range []string{
		"memory-limit: unlimited",
		"enable-unassign-security-group: true",
		`isolation_segment: ""`,
		"metadata: null",
		"users:\n  - ePayOctoSvcFOG",
	} {
		if !strings.Contains(out, sentinel) {
			t.Errorf("expected preserved field %q missing from output:\n%s", sentinel, out)
		}
	}
}

func TestProd_AddDeveloper_MinimalDiff(t *testing.T) {
	out, err := AddUserToSpaceRole([]byte(prodEpaySpaceConfig), RoleDeveloper, OriginLDAP, "newdev")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	assertFidelity(t, prodEpaySpaceConfig, s)
	if !strings.Contains(s, "- newdev") {
		t.Errorf("expected new developer in output")
	}
	if got, want := lineCount(s), lineCount(prodEpaySpaceConfig)+1; got != want {
		t.Errorf("expected exactly 1 line added: got %d lines, want %d", got, want)
	}
}

func TestProd_RemoveAuditor_MinimalDiff(t *testing.T) {
	out, err := RemoveUserFromSpaceRole([]byte(prodEpaySpaceConfig), RoleAuditor, OriginLDAP, "rskumar")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	assertFidelity(t, prodEpaySpaceConfig, s)
	// rskumar is in BOTH developer and auditor; removing from auditor leaves one.
	if strings.Count(s, "- rskumar\n") != 1 {
		t.Errorf("expected exactly one rskumar remaining (developer), got %d", strings.Count(s, "- rskumar\n"))
	}
	if got, want := lineCount(s), lineCount(prodEpaySpaceConfig)-1; got != want {
		t.Errorf("expected exactly 1 line removed: got %d lines, want %d", got, want)
	}
}

func TestProd_NewSupporter_CreatesBlockInOrder(t *testing.T) {
	out, err := AddUserToSpaceRole([]byte(prodEpaySpaceConfig), RoleSupporter, OriginLDAP, "supporta")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// Now a supporter block SHOULD exist, placed after auditor, before allow-ssh.
	ai := strings.Index(s, "space-auditor")
	si := strings.Index(s, "space-supporter")
	ssh := strings.Index(s, "allow-ssh")
	if si < 0 || !(ai < si && si < ssh) {
		t.Errorf("space-supporter not placed between auditor and allow-ssh:\nauditor=%d supporter=%d ssh=%d", ai, si, ssh)
	}
	if !strings.Contains(s, "- supporta") {
		t.Errorf("expected supporta in new supporter block")
	}
}

func TestProd_NetNoOpBatch_ReturnsOriginalBytes(t *testing.T) {
	ops := []Op{
		{User: "schavan", Origin: OriginLDAP, Role: RoleDeveloper, Action: "add"},    // already present
		{User: "ghost", Origin: OriginLDAP, Role: RoleManager, Action: "remove"},     // absent
	}
	out, err := ApplySpaceUserOps([]byte(prodEpaySpaceConfig), ops)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != prodEpaySpaceConfig {
		t.Errorf("expected original bytes returned unchanged for net no-op batch")
	}
}
