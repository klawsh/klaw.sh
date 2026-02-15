package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// Definition represents an agent definition.
type Definition struct {
	Name      string    `toml:"name"`
	Model     string    `toml:"model"`
	Task      string    `toml:"task"`
	Tools     []string  `toml:"tools"`
	WorkDir   string    `toml:"workdir"`
	Runtime   string    `toml:"runtime"` // "process" or "docker"
	CreatedAt time.Time `toml:"created_at"`
}

// DefinitionStore manages agent definitions.
type DefinitionStore struct {
	dir string
}

// NewDefinitionStore creates a new definition store.
func NewDefinitionStore(dir string) *DefinitionStore {
	return &DefinitionStore{dir: dir}
}

// Save saves an agent definition.
func (s *DefinitionStore) Save(def *Definition) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return fmt.Errorf("failed to create agents dir: %w", err)
	}

	path := filepath.Join(s.dir, def.Name+".toml")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create agent file: %w", err)
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	return encoder.Encode(def)
}

// Load loads an agent definition by name.
func (s *DefinitionStore) Load(name string) (*Definition, error) {
	path := filepath.Join(s.dir, name+".toml")

	var def Definition
	if _, err := toml.DecodeFile(path, &def); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("agent not found: %s", name)
		}
		return nil, fmt.Errorf("failed to load agent: %w", err)
	}

	return &def, nil
}

// List returns all agent definitions.
func (s *DefinitionStore) List() ([]*Definition, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var defs []*Definition
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".toml" {
			continue
		}

		name := e.Name()[:len(e.Name())-5] // Remove .toml
		def, err := s.Load(name)
		if err != nil {
			continue
		}
		defs = append(defs, def)
	}

	return defs, nil
}

// Delete deletes an agent definition.
func (s *DefinitionStore) Delete(name string) error {
	path := filepath.Join(s.dir, name+".toml")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("agent not found: %s", name)
		}
		return err
	}
	return nil
}

// Exists checks if an agent definition exists.
func (s *DefinitionStore) Exists(name string) bool {
	path := filepath.Join(s.dir, name+".toml")
	_, err := os.Stat(path)
	return err == nil
}
