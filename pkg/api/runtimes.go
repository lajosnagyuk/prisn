package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/store"
)

// getNamespaceFromRequest extracts namespace from query params with default.
func getNamespaceFromRequest(r *http.Request) string {
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = "default"
	}
	return ns
}

// CreateRuntimeRequest is the request body for creating a runtime.
type CreateRuntimeRequest struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Command    string            `json:"command"`
	Args       []string          `json:"args"`
	Extensions []string          `json:"extensions"`
	Env        map[string]string `json:"env,omitempty"`
	Detect     struct {
		Check        string `json:"check,omitempty"`
		VersionRegex string `json:"version_regex,omitempty"`
		MinVersion   string `json:"min_version,omitempty"`
	} `json:"detect,omitempty"`
	Install struct {
		MacOS   string `json:"macos,omitempty"`
		Linux   string `json:"linux,omitempty"`
		Windows string `json:"windows,omitempty"`
		Script  string `json:"script,omitempty"`
		DocURL  string `json:"doc_url,omitempty"`
		Note    string `json:"note,omitempty"`
	} `json:"install,omitempty"`
	Deps struct {
		Enabled        bool     `json:"enabled"`
		ManifestFiles  []string `json:"manifest_files,omitempty"`
		Parser         string   `json:"parser,omitempty"`
		InstallCommand string   `json:"install_command,omitempty"`
		SelfManaged    bool     `json:"self_managed,omitempty"`
		EnvType        string   `json:"env_type,omitempty"`
	} `json:"deps,omitempty"`
	Shebang []string `json:"shebang_patterns,omitempty"`
}

func (s *Server) handleListRuntimes(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = getNamespaceFromRequest(r)
	}

	runtimes, err := s.store.ListRuntimes(namespace)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeJSON(w, map[string]any{
		"items": runtimes,
		"count": len(runtimes),
	})
}

func (s *Server) handleCreateRuntime(w http.ResponseWriter, r *http.Request) {
	var req CreateRuntimeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if req.ID == "" {
		s.writeError(w, http.StatusBadRequest, errors.New("id is required"))
		return
	}
	if req.Name == "" {
		req.Name = req.ID
	}
	if req.Command == "" {
		s.writeError(w, http.StatusBadRequest, errors.New("command is required"))
		return
	}

	namespace := getNamespaceFromRequest(r)

	rt := &store.RuntimeDefinition{
		ID:         req.ID,
		Name:       req.Name,
		Namespace:  namespace,
		Command:    req.Command,
		Args:       req.Args,
		Extensions: req.Extensions,
		Env:        req.Env,
		Detect: store.RuntimeDetect{
			Check:        req.Detect.Check,
			VersionRegex: req.Detect.VersionRegex,
			MinVersion:   req.Detect.MinVersion,
		},
		Install: store.RuntimeInstall{
			MacOS:   req.Install.MacOS,
			Linux:   req.Install.Linux,
			Windows: req.Install.Windows,
			Script:  req.Install.Script,
			DocURL:  req.Install.DocURL,
			Note:    req.Install.Note,
		},
		Deps: store.RuntimeDeps{
			Enabled:        req.Deps.Enabled,
			ManifestFiles:  req.Deps.ManifestFiles,
			Parser:         req.Deps.Parser,
			InstallCommand: req.Deps.InstallCommand,
			SelfManaged:    req.Deps.SelfManaged,
			EnvType:        req.Deps.EnvType,
		},
		Shebang:   req.Shebang,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.store.CreateRuntime(rt); err != nil {
		if errors.As(err, new(*store.AlreadyExistsError)) {
			s.writeError(w, http.StatusConflict, err)
			return
		}
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusCreated)
	s.writeJSON(w, rt)
}

func (s *Server) handleGetRuntime(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	namespace := getNamespaceFromRequest(r)

	rt, err := s.store.GetRuntime(namespace, id)
	if err != nil {
		if errors.As(err, new(*store.NotFoundError)) {
			s.writeError(w, http.StatusNotFound, err)
			return
		}
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeJSON(w, rt)
}

func (s *Server) handleUpdateRuntime(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	namespace := getNamespaceFromRequest(r)

	// Check runtime exists
	existing, err := s.store.GetRuntime(namespace, id)
	if err != nil {
		if errors.As(err, new(*store.NotFoundError)) {
			s.writeError(w, http.StatusNotFound, err)
			return
		}
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	var req CreateRuntimeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	// Update fields
	existing.Name = req.Name
	if req.Name == "" {
		existing.Name = id
	}
	existing.Command = req.Command
	existing.Args = req.Args
	existing.Extensions = req.Extensions
	existing.Env = req.Env
	existing.Detect = store.RuntimeDetect{
		Check:        req.Detect.Check,
		VersionRegex: req.Detect.VersionRegex,
		MinVersion:   req.Detect.MinVersion,
	}
	existing.Install = store.RuntimeInstall{
		MacOS:   req.Install.MacOS,
		Linux:   req.Install.Linux,
		Windows: req.Install.Windows,
		Script:  req.Install.Script,
		DocURL:  req.Install.DocURL,
		Note:    req.Install.Note,
	}
	existing.Deps = store.RuntimeDeps{
		Enabled:        req.Deps.Enabled,
		ManifestFiles:  req.Deps.ManifestFiles,
		Parser:         req.Deps.Parser,
		InstallCommand: req.Deps.InstallCommand,
		SelfManaged:    req.Deps.SelfManaged,
		EnvType:        req.Deps.EnvType,
	}
	existing.Shebang = req.Shebang
	existing.UpdatedAt = time.Now()

	if err := s.store.UpdateRuntime(existing); err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	s.writeJSON(w, existing)
}

func (s *Server) handleDeleteRuntime(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	namespace := getNamespaceFromRequest(r)

	if err := s.store.DeleteRuntime(namespace, id); err != nil {
		if errors.As(err, new(*store.NotFoundError)) {
			s.writeError(w, http.StatusNotFound, err)
			return
		}
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
