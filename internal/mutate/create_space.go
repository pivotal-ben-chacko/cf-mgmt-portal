package mutate

import (
	"fmt"
	"path"

	"github.com/vmwarepivotallabs/cf-mgmt/config"
	"gopkg.in/yaml.v2"
)

// FileChange describes one file in a multi-file mutation.
type FileChange struct {
	Path    string
	Content []byte
	IsNew   bool
}

// CreateSpace produces the set of files needed to add a new space to an
// existing org:
//
//   - <foundationDir>/<org>/spaces.yml — updated to include spaceName
//   - <foundationDir>/<org>/<spaceName>/spaceConfig.yml — new, built from defaults
//   - <foundationDir>/<org>/<spaceName>/security-group.json — new, "[]"
//
// If the space already exists in spaces.yml, returns nil to signal no-op.
// If defaults is nil, the new spaceConfig.yml is minimal (org and space only).
func CreateSpace(
	currentSpacesYAML []byte,
	foundationDir, org, spaceName string,
	defaults *config.SpaceConfig,
) ([]FileChange, error) {
	var spaces config.Spaces
	if err := yaml.Unmarshal(currentSpacesYAML, &spaces); err != nil {
		return nil, fmt.Errorf("parse spaces.yml: %w", err)
	}
	if spaces.Contains(spaceName) {
		return nil, nil
	}
	spaces.Spaces = append(spaces.Spaces, spaceName)

	updatedSpaces, err := yaml.Marshal(&spaces)
	if err != nil {
		return nil, fmt.Errorf("marshal spaces.yml: %w", err)
	}

	cfg := config.SpaceConfig{}
	if defaults != nil {
		cfg = *defaults
	}
	cfg.Org = org
	cfg.Space = spaceName

	spaceConfigYAML, err := yaml.Marshal(&cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal spaceConfig.yml: %w", err)
	}

	spaceDir := path.Join(foundationDir, org, spaceName)
	return []FileChange{
		{Path: path.Join(foundationDir, org, "spaces.yml"), Content: updatedSpaces, IsNew: false},
		{Path: path.Join(spaceDir, "spaceConfig.yml"), Content: spaceConfigYAML, IsNew: true},
		{Path: path.Join(spaceDir, "security-group.json"), Content: []byte("[]"), IsNew: true},
	}, nil
}
