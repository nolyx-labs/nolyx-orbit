package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"orbit/internal/protocol"
)

var appNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type appInput struct {
	Name           *string           `json:"name"`
	ServerID       *string           `json:"server_id"`
	Image          *string           `json:"image"`
	Ports          []string          `json:"ports"`
	HealthPath     *string           `json:"health_path"`
	Env            map[string]string `json:"env"`       // merged into existing
	UnsetEnv       []string          `json:"unset_env"` // removed
	Proxy          *bool             `json:"proxy"`
	CPUs           *float64          `json:"cpus"`
	MemoryMB       *int              `json:"memory_mb"`
	Orchestrator   *string           `json:"orchestrator"`
	NomadClusterID *string           `json:"nomad_cluster_id"`
	Instances      *int              `json:"instances"`
	RepoURL        *string           `json:"repo_url"`
	Branch         *string           `json:"branch"`
	Dockerfile     *string           `json:"dockerfile"`
	BuildContext   *string           `json:"build_context"`
	RepoToken      *string           `json:"repo_token"` // write-only; "" clears it
	AutoDeploy     *bool             `json:"auto_deploy"`
}

var pathRe = regexp.MustCompile(`^[A-Za-z0-9._/-]*$`)

func (in appInput) validate() error {
	if in.Name != nil && !appNameRe.MatchString(*in.Name) {
		return errStatus(400, "name must be lowercase letters, digits and dashes")
	}
	if in.Image != nil && strings.ContainsAny(*in.Image, " \t\n") {
		return errStatus(400, "invalid image")
	}
	for _, p := range in.Ports {
		if _, _, err := protocol.ParsePort(p); err != nil {
			return errStatus(400, "%v", err)
		}
	}
	for k := range in.Env {
		if !envKeyRe.MatchString(k) {
			return errStatus(400, "invalid environment variable name %q", k)
		}
	}
	if in.HealthPath != nil && *in.HealthPath != "" && !strings.HasPrefix(*in.HealthPath, "/") {
		return errStatus(400, "health check path must start with /")
	}
	if in.Orchestrator != nil && *in.Orchestrator != "docker" && *in.Orchestrator != "nomad" {
		return errStatus(400, "orchestrator must be docker or nomad")
	}
	if in.Instances != nil && (*in.Instances < 1 || *in.Instances > 100) {
		return errStatus(400, "instances must be 1–100")
	}
	if (in.CPUs != nil && (*in.CPUs < 0 || *in.CPUs > 256)) || (in.MemoryMB != nil && *in.MemoryMB < 0) {
		return errStatus(400, "invalid resources")
	}
	if in.RepoURL != nil && *in.RepoURL != "" {
		u, err := url.Parse(*in.RepoURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return errStatus(400, "repository must be an https URL without credentials (use the access token field)")
		}
	}
	for _, p := range []*string{in.Dockerfile, in.BuildContext, in.Branch} {
		if p != nil && (!pathRe.MatchString(*p) || strings.Contains(*p, "..")) {
			return errStatus(400, "invalid branch, Dockerfile or build context")
		}
	}
	return nil
}

func (s *Server) appCols() string {
	return `a.id, a.name, a.server_id, s.name as server_name, a.image, a.ports, a.env_keys, a.health_path, a.created_at,
	a.proxy, a.cpus, a.memory_mb, a.orchestrator, a.nomad_cluster_id, a.instances, a.repo_url, a.branch, a.dockerfile, a.build_context,
	a.repo_token_encrypted is not null as has_repo_token, a.auto_deploy,
	case when a.repo_url <> '' then '` + strings.ReplaceAll(s.cfg.AppURL, "'", "") + `/hooks/github/' || a.id end as webhook_url,
	(select row_to_json(c) from (select docker_id as id, state, status, health, cpu, mem_used from containers c
	  where c.server_id = a.server_id and c.labels->>'orbit.app' = a.name order by created desc limit 1) c) as container,
	(select row_to_json(d) from (select id, number, status, created_at, commit_sha from deployments d where d.application_id = a.id order by number desc limit 1) d) as last_deployment`
}

func (s *Server) listApps(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select `+s.appCols()+` from applications a left join servers s on s.id = a.server_id where a.project_id = $1 order by a.name`, p.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) getApp(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	// The webhook secret is shown to members so they can configure GitHub.
	out, err := s.one(r.Context(), `select `+s.appCols()+`, case when $3 then a.webhook_secret end as webhook_secret
		from applications a left join servers s on s.id = a.server_id where a.project_id = $1 and a.id::text = $2`,
		p.ID, r.PathValue("aid"), roleRank[p.Role] >= roleRank["member"])
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) checkCluster(ctx context.Context, p project, id string) error {
	var ok bool
	s.db.QueryRow(ctx, `select exists(select 1 from nomad_clusters where project_id = $1 and id::text = $2)`, p.ID, id).Scan(&ok)
	if !ok {
		return errStatus(400, "Nomad cluster not found in this project")
	}
	return nil
}

func sortedKeys(m map[string]string) []byte {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b, _ := json.Marshal(keys)
	return b
}

func (s *Server) loadEnv(ctx context.Context, appID string) (map[string]string, error) {
	var enc []byte
	if err := s.db.QueryRow(ctx, `select env_encrypted from applications where id::text = $1`, appID).Scan(&enc); err != nil {
		return nil, err
	}
	env := map[string]string{}
	if enc == nil {
		return env, nil
	}
	plain, err := s.decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("decrypt env (wrong ENCRYPTION_KEY?): %w", err)
	}
	return env, json.Unmarshal(plain, &env)
}

// appSets turns the optional inputs into SQL assignments (shared by create and update).
func (s *Server) appSets(in appInput) (cols []string, vals []any) {
	add := func(c string, v any) { cols, vals = append(cols, c), append(vals, v) }
	if in.ServerID != nil {
		add("server_id", nilIfEmpty(*in.ServerID))
	}
	if in.Image != nil {
		add("image", *in.Image)
	}
	if in.Ports != nil {
		add("ports", mustJSON(in.Ports))
	}
	if in.HealthPath != nil {
		add("health_path", *in.HealthPath)
	}
	if in.Proxy != nil {
		add("proxy", *in.Proxy)
	}
	if in.CPUs != nil {
		add("cpus", *in.CPUs)
	}
	if in.MemoryMB != nil {
		add("memory_mb", *in.MemoryMB)
	}
	if in.Orchestrator != nil {
		add("orchestrator", *in.Orchestrator)
	}
	if in.NomadClusterID != nil {
		add("nomad_cluster_id", nilIfEmpty(*in.NomadClusterID))
	}
	if in.Instances != nil {
		add("instances", *in.Instances)
	}
	if in.RepoURL != nil {
		add("repo_url", *in.RepoURL)
	}
	if in.Branch != nil {
		add("branch", *in.Branch)
	}
	if in.Dockerfile != nil {
		add("dockerfile", *in.Dockerfile)
	}
	if in.BuildContext != nil {
		add("build_context", *in.BuildContext)
	}
	if in.RepoToken != nil {
		if *in.RepoToken == "" {
			add("repo_token_encrypted", nil)
		} else {
			add("repo_token_encrypted", s.encrypt([]byte(*in.RepoToken)))
		}
	}
	if in.AutoDeploy != nil {
		add("auto_deploy", *in.AutoDeploy)
	}
	return
}

// checkAppTargets validates the server / cluster references and the git + Nomad combination.
func (s *Server) checkAppTargets(ctx context.Context, p project, appID string) error {
	var orch, repo string
	var server, cluster *string
	if err := s.db.QueryRow(ctx, `select orchestrator, repo_url, server_id::text, nomad_cluster_id::text from applications where id::text = $1`, appID).
		Scan(&orch, &repo, &server, &cluster); err != nil {
		return err
	}
	if orch == "nomad" {
		if cluster == nil {
			return errStatus(400, "choose a Nomad cluster")
		}
		if repo != "" {
			return errStatus(400, "Git builds produce an image on one server; Nomad services need a registry image")
		}
		return s.checkCluster(ctx, p, *cluster)
	}
	if server == nil {
		return errStatus(400, "choose a server")
	}
	return s.checkServer(ctx, p, *server)
}

func (s *Server) createApp(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var in appInput
	if err := decode(r, &in); err != nil {
		return err
	}
	if in.Name == nil {
		return errStatus(400, "name is required")
	}
	if (in.Image == nil || *in.Image == "") && (in.RepoURL == nil || *in.RepoURL == "") {
		return errStatus(400, "an image or a Git repository is required")
	}
	if err := in.validate(); err != nil {
		return err
	}
	if in.Image == nil || *in.Image == "" {
		img := "orbit/" + *in.Name
		in.Image = &img
	}
	if in.Env == nil {
		in.Env = map[string]string{}
	}
	envJSON, _ := json.Marshal(in.Env)
	cols, vals := s.appSets(in)
	cols = append(cols, "project_id", "name", "env_encrypted", "env_keys", "webhook_secret")
	vals = append(vals, p.ID, *in.Name, s.encrypt(envJSON), sortedKeys(in.Env), randomSecret())
	ph := make([]string, len(vals))
	for i := range vals {
		ph[i] = fmt.Sprintf("$%d", i+1)
	}
	var id string
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		err := tx.QueryRow(r.Context(), `insert into applications (`+strings.Join(cols, ", ")+`) values (`+strings.Join(ph, ", ")+`)
			on conflict do nothing returning id::text`, vals...).Scan(&id)
		if err == pgx.ErrNoRows {
			return errStatus(409, "an application named %s already exists", *in.Name)
		}
		return err
	})
	if err != nil {
		return err
	}
	if err := s.checkAppTargets(r.Context(), p, id); err != nil {
		s.db.Exec(r.Context(), `delete from applications where id = $1`, id)
		return err
	}
	s.auditProject(r, p, "app.create", *in.Name, map[string]any{"image": *in.Image})
	writeJSON(w, 201, map[string]string{"id": id})
	return nil
}

func (s *Server) updateApp(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var in appInput
	if err := decode(r, &in); err != nil {
		return err
	}
	in.Name = nil // renames would orphan containers labeled with the old name
	if err := in.validate(); err != nil {
		return err
	}
	aid := r.PathValue("aid")
	var name string
	if err := s.db.QueryRow(r.Context(), `select name from applications where project_id = $1 and id::text = $2`, p.ID, aid).Scan(&name); err != nil {
		return err
	}
	env, err := s.loadEnv(r.Context(), aid)
	if err != nil {
		return err
	}
	for k, v := range in.Env {
		env[k] = v
	}
	for _, k := range in.UnsetEnv {
		delete(env, k)
	}
	envJSON, _ := json.Marshal(env)
	cols, vals := s.appSets(in)
	cols = append(cols, "env_encrypted", "env_keys")
	vals = append(vals, s.encrypt(envJSON), sortedKeys(env))
	sets := make([]string, len(cols))
	for i, c := range cols {
		sets[i] = fmt.Sprintf("%s = $%d", c, i+3)
	}
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `update applications set `+strings.Join(sets, ", ")+` where project_id = $1 and id::text = $2`,
			append([]any{p.ID, aid}, vals...)...); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.checkAppTargets(r.Context(), p, aid); err != nil {
		return err
	}
	s.auditProject(r, p, "app.update", name, map[string]any{"env_keys_changed": len(in.Env)+len(in.UnsetEnv) > 0, "fields": cols[:len(cols)-2]})
	return s.getApp(w, r)
}

func (s *Server) deleteApp(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `select name from applications where project_id = $1 and id::text = $2`, p.ID, r.PathValue("aid")).Scan(&name); err != nil {
		return err
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := decode(r, &body); err != nil || body.Confirm != name {
		return errStatus(400, "type the service name to confirm")
	}
	s.db.Exec(r.Context(), `delete from applications where id::text = $1`, r.PathValue("aid"))
	s.auditProject(r, p, "app.delete", name, nil)
	w.WriteHeader(204)
	return nil
}
