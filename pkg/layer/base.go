package layer

import (
	"fmt"
	"os"
	"path/filepath"
)

// BaseDefinition defines a pre-built base environment.
type BaseDefinition struct {
	Name        string
	Description string
	Packages    []Package
	Priority    int // Higher = more specific, prefer for matching
}

// DefaultBases are the pre-built bases we ship.
var DefaultBases = []BaseDefinition{
	{
		Name:        "python-minimal",
		Description: "Minimal Python with common utilities",
		Priority:    1,
		Packages: []Package{
			{Name: "pip"},
			{Name: "setuptools"},
			{Name: "wheel"},
		},
	},
	{
		Name:        "python-web",
		Description: "Web development (Flask, requests, APIs)",
		Priority:    10,
		Packages: []Package{
			{Name: "flask"},
			{Name: "requests"},
			{Name: "gunicorn"},
			{Name: "httpx"},
			{Name: "urllib3"},
			{Name: "certifi"},
			{Name: "charset-normalizer"},
			{Name: "idna"},
			{Name: "click"},
			{Name: "itsdangerous"},
			{Name: "jinja2"},
			{Name: "markupsafe"},
			{Name: "werkzeug"},
			{Name: "python-dotenv"},
		},
	},
	{
		Name:        "python-api",
		Description: "API development (FastAPI, Pydantic)",
		Priority:    15,
		Packages: []Package{
			{Name: "fastapi"},
			{Name: "uvicorn"},
			{Name: "pydantic"},
			{Name: "starlette"},
			{Name: "httpx"},
			{Name: "requests"},
			{Name: "python-multipart"},
			{Name: "python-dotenv"},
			{Name: "typing-extensions"},
		},
	},
	{
		Name:        "python-data",
		Description: "Data science (pandas, numpy)",
		Priority:    10,
		Packages: []Package{
			{Name: "pandas"},
			{Name: "numpy"},
			{Name: "scipy"},
			{Name: "matplotlib"},
			{Name: "seaborn"},
			{Name: "scikit-learn"},
			{Name: "requests"},
			{Name: "python-dateutil"},
			{Name: "pytz"},
		},
	},
	{
		Name:        "python-automation",
		Description: "Scripting and automation",
		Priority:    8,
		Packages: []Package{
			{Name: "requests"},
			{Name: "beautifulsoup4"},
			{Name: "lxml"},
			{Name: "pyyaml"},
			{Name: "toml"},
			{Name: "python-dotenv"},
			{Name: "click"},
			{Name: "rich"},
			{Name: "httpx"},
		},
	},
	{
		Name:        "python-database",
		Description: "Database access",
		Priority:    12,
		Packages: []Package{
			{Name: "sqlalchemy"},
			{Name: "psycopg2-binary"},
			{Name: "pymysql"},
			{Name: "redis"},
			{Name: "pymongo"},
			{Name: "python-dotenv"},
		},
	},
}

// BuildBase creates a base layer from a definition.
func (s *Store) BuildBase(def BaseDefinition) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if already exists
	if existing, ok := s.bases[def.Name]; ok {
		// Verify the layer still exists
		if _, layerOk := s.layers[existing.LayerID]; layerOk {
			return nil // Already built
		}
		// Layer is gone, rebuild
	}

	fmt.Printf("Building base: %s (%s)\n", def.Name, def.Description)

	// Create the layer
	layer, err := s.createLayer(computeLayerID(def.Packages), def.Packages)
	if err != nil {
		return fmt.Errorf("build base %s: %w", def.Name, err)
	}

	s.layers[layer.ID] = layer

	// Register the base
	base := &Base{
		Name:        def.Name,
		Description: def.Description,
		LayerID:     layer.ID,
		Packages:    def.Packages,
		Priority:    def.Priority,
	}
	s.bases[def.Name] = base

	return s.saveIndex()
}

// BuildAllBases builds all default bases.
func (s *Store) BuildAllBases() error {
	for _, def := range DefaultBases {
		if err := s.BuildBase(def); err != nil {
			return err
		}
	}
	return nil
}

// EnsureBasesExist checks if bases exist and builds missing ones.
func (s *Store) EnsureBasesExist() error {
	s.mu.RLock()
	needsBuild := len(s.bases) == 0
	s.mu.RUnlock()

	if needsBuild {
		return s.BuildAllBases()
	}
	return nil
}

// BaseStatus returns the status of all bases.
func (s *Store) BaseStatus() []BaseInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var infos []BaseInfo
	for _, def := range DefaultBases {
		info := BaseInfo{
			Name:        def.Name,
			Description: def.Description,
			Packages:    len(def.Packages),
			Built:       false,
		}

		if base, ok := s.bases[def.Name]; ok {
			if layer, layerOk := s.layers[base.LayerID]; layerOk {
				info.Built = true
				info.Size = layer.Size
				info.Path = layer.Path
			}
		}

		infos = append(infos, info)
	}
	return infos
}

// BaseInfo provides information about a base.
type BaseInfo struct {
	Name        string
	Description string
	Packages    int
	Built       bool
	Size        int64
	Path        string
}

// ImportBase imports a base from a tarball (for airgapped environments).
func (s *Store) ImportBase(name string, tarPath string) error {
	// Find the definition
	var def *BaseDefinition
	for _, d := range DefaultBases {
		if d.Name == name {
			def = &d
			break
		}
	}
	if def == nil {
		return fmt.Errorf("unknown base: %s", name)
	}

	layerID := computeLayerID(def.Packages)
	layerPath := filepath.Join(s.root, "layers", layerID)

	// Extract tarball to layer path
	if err := os.MkdirAll(layerPath, 0755); err != nil {
		return err
	}

	cmd := newCommand("tar", "-xf", tarPath, "-C", layerPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("extract %s: %s\n%s", tarPath, err, output)
	}

	size, _ := dirSize(layerPath)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Register layer and base
	layer := &Layer{
		ID:       layerID,
		Packages: def.Packages,
		Size:     size,
		Path:     layerPath,
	}
	s.layers[layerID] = layer

	base := &Base{
		Name:        def.Name,
		Description: def.Description,
		LayerID:     layerID,
		Packages:    def.Packages,
		Priority:    def.Priority,
	}
	s.bases[def.Name] = base

	return s.saveIndex()
}

// ExportBase exports a base to a tarball.
func (s *Store) ExportBase(name string, tarPath string) error {
	s.mu.RLock()
	base, ok := s.bases[name]
	if !ok {
		s.mu.RUnlock()
		return fmt.Errorf("base not found: %s", name)
	}
	layer, ok := s.layers[base.LayerID]
	if !ok {
		s.mu.RUnlock()
		return fmt.Errorf("layer not found for base: %s", name)
	}
	layerPath := layer.Path
	s.mu.RUnlock()

	cmd := newCommand("tar", "-cf", tarPath, "-C", layerPath, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create tarball: %s\n%s", err, output)
	}

	return nil
}
