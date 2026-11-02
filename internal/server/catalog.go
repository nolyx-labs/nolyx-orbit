// One-click app catalog: templates (catalog_apps.go) turned into ready-to-deploy services.
package server

import (
	"context"
	"crypto/rand"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"orbit/internal/protocol"
)

type catalogPort struct {
	Container string `json:"container"` // "80" or "19132/udp"
	Host      int    `json:"host"`      // suggested host port
	Label     string `json:"label"`
}

type catalogInput struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"`              // text | email | url | checkbox (sent as "true"/"false", set as TRUE/FALSE)
	Default  string `json:"default,omitempty"` // {url} and {host} become the server's address and first host port
	Required bool   `json:"required,omitempty"`
	Hint     string `json:"hint,omitempty"`
}

type catalogSecret struct {
	Key, Label string
	Length     int // default 32
}

type catalogCompanion struct {
	Suffix    string            `json:"suffix"` // service name is <name>-<suffix>
	App       string            `json:"app"`    // catalog id providing image, volumes, command and health check
	Label     string            `json:"label"`
	Optional  bool              `json:"optional,omitempty"`
	Env       map[string]string `json:"-"` // merged over the companion app's env
	ParentEnv map[string]string `json:"-"` // added to the main service's env when the companion is installed
}

// Env and Command values may reference ${secret:key} (generated once per install, shared by its services),
// ${input:key}, ${service} (the main service name) and ${service:suffix} (a companion's service name).
type catalogApp struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Category    string             `json:"category"`
	Description string             `json:"description"`
	Website     string             `json:"website"`
	Image       string             `json:"image,omitempty"`
	Ports       []catalogPort      `json:"ports"`
	Volumes     []string           `json:"volumes"` // "name:/container/path"
	Health      string             `json:"health,omitempty"`
	Inputs      []catalogInput     `json:"inputs"`
	Notes       string             `json:"notes,omitempty"`
	Companions  []catalogCompanion `json:"companions"`
	Link        string             `json:"link,omitempty"` // handled elsewhere in the UI (project-relative path); not installable here
	Env         map[string]string  `json:"-"`
	Command     []string           `json:"-"`
	HealthCmd   string             `json:"-"`
	Secrets     []catalogSecret    `json:"-"`
}

type catalogCategory struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func findCatalogApp(id string) (catalogApp, bool) {
	for _, a := range catalogApps {
		if a.ID == id {
			return a, true
		}
	}
	return catalogApp{}, false
}

type plannedService struct {
	Name, Image, Health, HealthCmd string
	Ports, Volumes, Command        []string
	Env                            map[string]string
}

type catalogCredential struct {
	Service string `json:"service"`
	Label   string `json:"label"`
	Value   string `json:"value"`
}

var catalogRef = regexp.MustCompile(`\$\{(secret|input|service)(?::([a-z0-9_]+))?\}`)

func genSecret(n int) string {
	if n == 0 {
		n = 32
	}
	s := ""
	for len(s) < n {
		s += rand.Text() // 26 base32 characters, safe in URLs, shells and config files
	}
	return s[:n]
}

// planInstall resolves an app (and its companions, which come first) into services. addr is the server's
// address, for input defaults. Secrets are generated here and returned once as credentials.
func planInstall(app catalogApp, name, addr string, hostPorts []int, inputs map[string]string, optional map[string]bool) ([]plannedService, []catalogCredential, error) {
	if !appNameRe.MatchString(name) {
		return nil, nil, errStatus(400, "name must be lowercase letters, digits and dashes")
	}
	if len(hostPorts) != len(app.Ports) {
		return nil, nil, errStatus(400, "expected %d host ports (0 to not publish one)", len(app.Ports))
	}
	seen := map[string]bool{}
	var ports []string
	for i, h := range hostPorts {
		if h == 0 {
			continue
		}
		p := strconv.Itoa(h) + ":" + app.Ports[i].Container
		host, ctr, err := protocol.ParsePort(p)
		if err != nil {
			return nil, nil, errStatus(400, "%v", err)
		}
		if k := host + strings.TrimLeft(ctr, "0123456789"); seen[k] {
			return nil, nil, errStatus(400, "host port %s is used twice", host)
		} else {
			seen[k] = true
		}
		ports = append(ports, p)
	}
	firstHost := ""
	if len(hostPorts) > 0 && hostPorts[0] != 0 {
		firstHost = strconv.Itoa(hostPorts[0])
	}
	vals, err := app.inputValues(inputs, addr, firstHost)
	if err != nil {
		return nil, nil, err
	}

	// Which companions are installed, and who declares each secret.
	names := map[string]string{"": name}
	owner := map[string]string{}
	label := map[string]catalogSecret{}
	var order []string
	declare := func(svc string, ss []catalogSecret) {
		for _, sc := range ss {
			if _, ok := owner[sc.Key]; !ok {
				owner[sc.Key], label[sc.Key] = svc, sc
				order = append(order, sc.Key)
			}
		}
	}
	declare(name, app.Secrets)
	var comps []catalogCompanion
	for _, c := range app.Companions {
		if c.Optional && !optional[c.Suffix] {
			continue
		}
		names[c.Suffix] = name + "-" + c.Suffix
		if !appNameRe.MatchString(names[c.Suffix]) {
			return nil, nil, errStatus(400, "name is too long")
		}
		base, _ := findCatalogApp(c.App)
		declare(names[c.Suffix], base.Secrets)
		comps = append(comps, c)
	}
	for k := range optional {
		if !slices.ContainsFunc(app.Companions, func(c catalogCompanion) bool { return c.Suffix == k }) {
			return nil, nil, errStatus(400, "unknown companion %q", k)
		}
	}

	secrets := map[string]string{}
	expand := func(s string) (string, error) {
		var bad error
		out := catalogRef.ReplaceAllStringFunc(s, func(ref string) string {
			m := catalogRef.FindStringSubmatch(ref)
			switch m[1] {
			case "secret":
				if _, ok := owner[m[2]]; !ok {
					bad = fmt.Errorf("undeclared secret %q", m[2])
				} else if secrets[m[2]] == "" {
					secrets[m[2]] = genSecret(label[m[2]].Length)
				}
				return secrets[m[2]]
			case "input":
				v, ok := vals[m[2]]
				if !ok {
					bad = fmt.Errorf("undeclared input %q", m[2])
				}
				return v
			}
			v, ok := names[m[2]]
			if !ok {
				bad = fmt.Errorf("companion %q is not installed", m[2])
			}
			return v
		})
		return out, bad
	}
	expandAll := func(env map[string]string, cmd []string) (map[string]string, []string, error) {
		outEnv := map[string]string{}
		for k, v := range env {
			e, err := expand(v)
			if err != nil {
				return nil, nil, err
			}
			outEnv[k] = e
		}
		var outCmd []string
		for _, v := range cmd {
			e, err := expand(v)
			if err != nil {
				return nil, nil, err
			}
			outCmd = append(outCmd, e)
		}
		return outEnv, outCmd, nil
	}

	var out []plannedService
	mainEnv := maps.Clone(app.Env)
	if mainEnv == nil {
		mainEnv = map[string]string{}
	}
	for _, c := range comps {
		base, _ := findCatalogApp(c.App)
		env := maps.Clone(base.Env)
		if env == nil {
			env = map[string]string{}
		}
		maps.Copy(env, c.Env)
		env, cmd, err := expandAll(env, base.Command)
		if err != nil {
			return nil, nil, err
		}
		// Companions are only reached over the orbit network, so they publish no ports.
		out = append(out, plannedService{Name: names[c.Suffix], Image: base.Image, HealthCmd: base.HealthCmd,
			Ports: []string{}, Volumes: orEmpty(base.Volumes), Command: orEmpty(cmd), Env: env})
		maps.Copy(mainEnv, c.ParentEnv)
	}
	env, cmd, err := expandAll(mainEnv, app.Command)
	if err != nil {
		return nil, nil, err
	}
	health := app.Health
	if firstHost == "" { // the agent health-checks the first published port
		health = ""
	}
	out = append(out, plannedService{Name: name, Image: app.Image, Health: health, HealthCmd: app.HealthCmd,
		Ports: orEmpty(ports), Volumes: orEmpty(app.Volumes), Command: orEmpty(cmd), Env: env})

	var creds []catalogCredential
	for _, k := range order {
		if v := secrets[k]; v != "" {
			creds = append(creds, catalogCredential{Service: owner[k], Label: label[k].Label, Value: v})
		}
	}
	return out, creds, nil
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// inputValues validates user inputs (they end up in container environments) and applies defaults.
func (app catalogApp) inputValues(in map[string]string, addr, firstHost string) (map[string]string, error) {
	out := map[string]string{}
	for k := range in {
		if !slices.ContainsFunc(app.Inputs, func(i catalogInput) bool { return i.Key == k }) {
			return nil, errStatus(400, "unknown input %q", k)
		}
	}
	for _, i := range app.Inputs {
		v, ok := in[i.Key]
		if !ok {
			v = i.Default
			if addr == "" {
				addr = "localhost"
			}
			v = strings.ReplaceAll(v, "{host}", addr)
			v = strings.ReplaceAll(v, "{url}", "http://"+addr+":"+firstHost)
		}
		v = strings.TrimSpace(v)
		if len(v) > 500 || strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return nil, errStatus(400, "%s: invalid value", i.Label)
		}
		switch i.Type {
		case "checkbox":
			if v != "true" && v != "false" && v != "" {
				return nil, errStatus(400, "%s: expected true or false", i.Label)
			}
			if i.Required && v != "true" {
				return nil, errStatus(400, "%s is required", i.Label)
			}
			v = strings.ToUpper(v)
			if v == "" {
				v = "FALSE"
			}
		case "email":
			if v != "" && (!strings.Contains(v, "@") || strings.ContainsAny(v, " ,;")) {
				return nil, errStatus(400, "%s: invalid email address", i.Label)
			}
		case "url":
			if v != "" {
				u, err := url.Parse(v)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return nil, errStatus(400, "%s: expected an http(s) URL", i.Label)
				}
			}
		}
		if i.Required && v == "" {
			return nil, errStatus(400, "%s is required", i.Label)
		}
		out[i.Key] = v
	}
	return out, nil
}

func (s *Server) listCatalog(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.projectAccess(r, "viewer"); err != nil {
		return err
	}
	// Empty lists go out as [] rather than null, so the UI can map over them.
	apps := slices.Clone(catalogApps)
	for i, a := range apps {
		apps[i].Ports, apps[i].Volumes, apps[i].Inputs, apps[i].Companions = orEmpty(a.Ports), orEmpty(a.Volumes), orEmpty(a.Inputs), orEmpty(a.Companions)
	}
	writeJSON(w, 200, map[string]any{"categories": catalogCategories, "apps": apps})
	return nil
}

// checkHostPorts rejects host ports another service or managed database on the server already publishes.
func (s *Server) checkHostPorts(ctx context.Context, serverID string, ports []string) error {
	rows, err := s.db.Query(ctx, `select name, ports from applications where server_id::text = $1 and orchestrator = 'docker'
		union all select 'database ' || name, jsonb_build_array(port::text) from databases where server_id::text = $1 and managed`, serverID)
	if err != nil {
		return err
	}
	used := map[string]string{}
	for rows.Next() {
		var name string
		var ps []string
		if err := rows.Scan(&name, &ps); err != nil {
			rows.Close()
			return err
		}
		for _, p := range ps {
			if h, c, err := protocol.ParsePort(p); err == nil {
				used[h+strings.TrimLeft(c, "0123456789")] = name
			}
		}
	}
	rows.Close()
	for _, p := range ports {
		h, c, _ := protocol.ParsePort(p)
		if name, ok := used[h+strings.TrimLeft(c, "0123456789")]; ok {
			return errStatus(409, "host port %s is already used by %s on this server", h, name)
		}
	}
	return nil
}

func (s *Server) installCatalogApp(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var in struct {
		App        string            `json:"app"`
		ServerID   string            `json:"server_id"`
		Name       string            `json:"name"`
		Ports      []int             `json:"ports"`
		Inputs     map[string]string `json:"inputs"`
		Companions map[string]bool   `json:"companions"` // optional companions to include, by suffix
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	app, ok := findCatalogApp(in.App)
	if !ok || app.Link != "" {
		return errStatus(400, "unknown catalog app")
	}
	if err := s.checkServer(r.Context(), p, in.ServerID); err != nil {
		return err
	}
	var addr string
	s.db.QueryRow(r.Context(), `select ip from servers where id::text = $1`, in.ServerID).Scan(&addr)
	svcs, creds, err := planInstall(app, in.Name, addr, in.Ports, in.Inputs, in.Companions)
	if err != nil {
		return err
	}
	if err := s.checkHostPorts(r.Context(), in.ServerID, svcs[len(svcs)-1].Ports); err != nil {
		return err
	}
	ids := make([]string, len(svcs))
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		for i, sv := range svcs {
			envJSON := mustJSON(sv.Env)
			err := tx.QueryRow(r.Context(), `insert into applications (project_id, name, server_id, image, ports, env_encrypted, env_keys,
				health_path, volumes, command, health_cmd, webhook_secret)
				values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) on conflict do nothing returning id::text`,
				p.ID, sv.Name, in.ServerID, sv.Image, mustJSON(sv.Ports), s.encrypt(envJSON), sortedKeys(sv.Env),
				sv.Health, mustJSON(sv.Volumes), mustJSON(sv.Command), sv.HealthCmd, randomSecret()).Scan(&ids[i])
			if err == pgx.ErrNoRows {
				return errStatus(409, "a service named %s already exists", sv.Name)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	names := make([]string, len(svcs))
	for i, sv := range svcs {
		names[i] = sv.Name
	}
	s.auditProject(r, p, "catalog.install", in.Name, map[string]any{"app": app.ID, "image": app.Image, "services": names, "server_id": in.ServerID})

	type result struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		DeploymentID string `json:"deployment_id,omitempty"`
		Waiting      bool   `json:"waiting,omitempty"` // deploys once its companions are healthy
		Error        string `json:"error,omitempty"`
	}
	u := currentUser(r)
	o := deployOpts{UserID: u.ID, Actor: u.label()}
	out := make([]result, len(svcs))
	var deps []string
	failed := false
	for i := range svcs {
		out[i] = result{ID: ids[i], Name: names[i]}
		if i == len(svcs)-1 && len(deps) > 0 && !failed {
			out[i].Waiting = true
			go s.deployAfter(context.WithoutCancel(r.Context()), p, deps, ids[i], names[i], o)
			break
		}
		if failed {
			out[i].Error = "not deployed: a companion service failed to start deploying"
			break
		}
		did, _, err := s.startDeploy(r.Context(), p, ids[i], o)
		if err != nil {
			out[i].Error = err.Error()
			failed = true
			continue
		}
		out[i].DeploymentID, deps = did, append(deps, did)
	}
	// The services exist even if a deploy couldn't start, and these credentials aren't shown again.
	writeJSON(w, 201, map[string]any{"services": out, "credentials": orEmpty(creds)})
	return nil
}

// deployAfter deploys a service once its companions' deployments succeeded, so an app never boots against
// a database that isn't accepting connections yet.
// shortcut: in-memory wait; if the control plane restarts meanwhile, the service stays undeployed until someone deploys it.
func (s *Server) deployAfter(ctx context.Context, p project, deps []string, appID, name string, o deployOpts) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	for {
		var running, failed int
		if err := s.db.QueryRow(ctx, `select count(*) filter (where status = 'running'), count(*) filter (where status = 'failed')
			from deployments where id::text = any($1)`, deps).Scan(&running, &failed); err != nil {
			s.event(ctx, p.ID, "", "error", fmt.Sprintf("%s was not deployed: %v", name, err))
			return
		}
		if failed > 0 {
			s.event(ctx, p.ID, "", "error", fmt.Sprintf("%s was not deployed because a companion service failed to deploy", name))
			return
		}
		if running == 0 {
			break
		}
		select {
		case <-ctx.Done():
			s.event(context.WithoutCancel(ctx), p.ID, "", "error", fmt.Sprintf("%s was not deployed: its companion services took too long", name))
			return
		case <-time.After(3 * time.Second):
		}
	}
	if _, _, err := s.startDeploy(ctx, p, appID, o); err != nil {
		s.event(ctx, p.ID, "", "error", fmt.Sprintf("%s was not deployed: %v", name, err))
	}
}
