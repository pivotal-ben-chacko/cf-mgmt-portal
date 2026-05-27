package mutate

import (
	"strings"
	"testing"

	"github.com/vmwarepivotallabs/cf-mgmt/config"
)

func TestCreateSpace_ReturnsThreeChangesForNewSpace(t *testing.T) {
	in := []byte(`org: epay-org
spaces:
  - existing-space
`)
	changes, err := CreateSpace(in, "fog", "epay-org", "new-space", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 {
		t.Fatalf("expected 3 changes, got %d", len(changes))
	}

	want := map[string]bool{
		"fog/epay-org/spaces.yml":                       false, // update
		"fog/epay-org/new-space/spaceConfig.yml":        true,  // new
		"fog/epay-org/new-space/security-group.json":   true,  // new
	}
	for _, c := range changes {
		isNew, ok := want[c.Path]
		if !ok {
			t.Errorf("unexpected change path: %s", c.Path)
			continue
		}
		if c.IsNew != isNew {
			t.Errorf("%s: IsNew = %v, want %v", c.Path, c.IsNew, isNew)
		}
	}
}

func TestCreateSpace_IdempotentWhenSpaceExists(t *testing.T) {
	in := []byte(`org: epay-org
spaces:
  - existing-space
`)
	changes, err := CreateSpace(in, "fog", "epay-org", "existing-space", nil)
	if err != nil {
		t.Fatal(err)
	}
	if changes != nil {
		t.Errorf("expected nil for existing space, got %d changes", len(changes))
	}
}

func TestCreateSpace_AppendsToSpacesList(t *testing.T) {
	in := []byte(`org: epay-org
spaces:
  - existing
`)
	changes, _ := CreateSpace(in, "fog", "epay-org", "newer", nil)
	for _, c := range changes {
		if c.Path == "fog/epay-org/spaces.yml" {
			body := string(c.Content)
			if !strings.Contains(body, "existing") || !strings.Contains(body, "newer") {
				t.Errorf("spaces.yml missing entries:\n%s", body)
			}
		}
	}
}

func TestCreateSpace_NewSpaceConfigSetsOrgAndSpace(t *testing.T) {
	in := []byte(`org: epay-org
spaces: []
`)
	changes, _ := CreateSpace(in, "fog", "epay-org", "newish", nil)
	for _, c := range changes {
		if c.Path == "fog/epay-org/newish/spaceConfig.yml" {
			body := string(c.Content)
			if !strings.Contains(body, "org: epay-org") {
				t.Errorf("spaceConfig missing org:\n%s", body)
			}
			if !strings.Contains(body, "space: newish") {
				t.Errorf("spaceConfig missing space:\n%s", body)
			}
		}
	}
}

func TestCreateSpace_EmptySecurityGroupJSON(t *testing.T) {
	in := []byte(`org: epay-org
spaces: []
`)
	changes, _ := CreateSpace(in, "fog", "epay-org", "newish", nil)
	var found bool
	for _, c := range changes {
		if c.Path == "fog/epay-org/newish/security-group.json" {
			found = true
			if string(c.Content) != "[]" {
				t.Errorf("security-group.json: %q", c.Content)
			}
		}
	}
	if !found {
		t.Error("no security-group.json in changes")
	}
}

func TestCreateSpace_AppliesDefaults(t *testing.T) {
	in := []byte(`org: epay-org
spaces: []
`)
	defaults := &config.SpaceConfig{
		AllowSSH:   true,
		IsoSegment: "shared",
	}
	changes, _ := CreateSpace(in, "fog", "epay-org", "newish", defaults)
	for _, c := range changes {
		if c.Path == "fog/epay-org/newish/spaceConfig.yml" {
			body := string(c.Content)
			if !strings.Contains(body, "allow-ssh: true") {
				t.Errorf("defaults not applied:\n%s", body)
			}
			if !strings.Contains(body, "isolation_segment: shared") {
				t.Errorf("isolation_segment default missing:\n%s", body)
			}
		}
	}
}
