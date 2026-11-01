package server

import (
	_ "embed"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

//go:embed install.sh
var installSh string

func (s *Server) installScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript")
	w.Write([]byte(strings.ReplaceAll(installSh, "{{APP_URL}}", s.cfg.AppURL)))
}

func (s *Server) downloadAgent(w http.ResponseWriter, r *http.Request) {
	f := r.PathValue("file")
	if f != "orbit-agent-linux-amd64" && f != "orbit-agent-linux-arm64" {
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(filepath.Join(s.cfg.AgentDist, f)); err != nil {
		http.Error(w, "agent binary not bundled with this build", 404)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, filepath.Join(s.cfg.AgentDist, f))
}
