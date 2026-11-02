// Deploying services (Docker, Git builds, Nomad), deployment history and releases.
package server

import (
	"context"
	"fmt"
	"net/http"
	"orbit/internal/protocol"
	"strings"
	"time"
)

type deployOpts struct {
	Image                                       string // override: redeploy/rollback/promote a specific image
	CommitSHA, CommitMessage, CommitAuthor, Ref string
	UserID, Actor                               string
}

type appRow struct {
	ID, Name, Image, Health, Orchestrator, RepoURL, Branch, Dockerfile, Context string
	Ports                                                                       []string
	ServerID, ClusterID                                                         *string
	Proxy                                                                       bool
	CPUs                                                                        float64
	MemoryMB, Instances                                                         int
	RepoToken                                                                   []byte
	Volumes, Command                                                            []string
	HealthCmd                                                                   string
}

func (s *Server) startDeploy(ctx context.Context, p project, appID string, o deployOpts) (string, int, error) {
	var a appRow
	err := s.db.QueryRow(ctx, `select id::text, name, image, health_path, orchestrator, repo_url, branch, dockerfile, build_context, ports,
		server_id::text, nomad_cluster_id::text, proxy, cpus, memory_mb, instances, repo_token_encrypted, volumes, command, health_cmd
		from applications where project_id = $1 and id::text = $2`, p.ID, appID).
		Scan(&a.ID, &a.Name, &a.Image, &a.Health, &a.Orchestrator, &a.RepoURL, &a.Branch, &a.Dockerfile, &a.Context, &a.Ports,
			&a.ServerID, &a.ClusterID, &a.Proxy, &a.CPUs, &a.MemoryMB, &a.Instances, &a.RepoToken, &a.Volumes, &a.Command, &a.HealthCmd)
	if err != nil {
		return "", 0, err
	}
	env, err := s.resolveEnv(ctx, p, appID)
	if err != nil {
		return "", 0, err
	}
	image := a.Image
	if o.Image != "" {
		image = o.Image
	}

	var serverID, action string
	var args any
	var plan []string
	if a.Orchestrator == "nomad" {
		if a.ClusterID == nil {
			return "", 0, errStatus(400, "service has no Nomad cluster")
		}
		if serverID, err = s.clusterAgent(ctx, p, *a.ClusterID); err != nil {
			return "", 0, err
		}
		job, err := nomadJob(a.Name, image, a.Ports, env, a.Health, a.Instances, a.CPUs, a.MemoryMB)
		if err != nil {
			return "", 0, err
		}
		action, args, plan = protocol.ActionNomadRegister, protocol.NomadJobArgs{JobID: a.Name, Job: job}, []string{"Submitting job", "Rolling out"}
	} else {
		if a.ServerID == nil {
			return "", 0, errStatus(400, "service has no target server")
		}
		serverID = *a.ServerID
		if !s.hub.connected(serverID) {
			return "", 0, errOffline
		}
		d := protocol.DeployArgs{App: a.Name, Image: image, Ports: a.Ports, Env: env, HealthPath: a.Health, Proxy: a.Proxy, CPUs: a.CPUs, MemoryMB: a.MemoryMB,
			Volumes: a.Volumes, Command: a.Command, HealthCmd: a.HealthCmd}
		if o.Image == "" && a.RepoURL != "" {
			branch := a.Branch
			if branch == "" {
				branch = "main"
			}
			if o.Ref == "" {
				o.Ref = branch
			}
			token := ""
			if a.RepoToken != nil {
				b, err := s.decrypt(a.RepoToken)
				if err != nil {
					return "", 0, err
				}
				token = string(b)
			}
			if o.CommitSHA == "" {
				o.CommitSHA, o.CommitMessage, o.CommitAuthor, _ = resolveCommit(ctx, a.RepoURL, o.Ref, token)
			}
			ref, tag := o.Ref, time.Now().UTC().Format("20060102150405")
			if o.CommitSHA != "" {
				ref, tag = o.CommitSHA, o.CommitSHA[:min(12, len(o.CommitSHA))]
			}
			d.Image = "orbit/" + a.Name + ":" + tag
			d.Build = &protocol.BuildArgs{Remote: gitRemote(a.RepoURL, token), Ref: ref, Context: a.Context, Dockerfile: a.Dockerfile}
			image = d.Image
		}
		action, args, plan = protocol.ActionDeploy, d, protocol.DeployPlan(d)
	}

	id := newUUID()
	var number int
	if err := s.db.QueryRow(ctx, `
		insert into deployments (id, application_id, number, server_id, image, triggered_by, plan, commit_sha, commit_message, commit_author, branch)
		values ($1, $2, (select coalesce(max(number),0) + 1 from deployments where application_id = $2), $3, $4, nullif($5,'')::uuid, $6, $7, $8, $9, $10)
		returning number`, id, a.ID, serverID, image, o.UserID, mustJSON(plan), o.CommitSHA, trunc(o.CommitMessage, 500), trunc(o.CommitAuthor, 200), o.Ref).Scan(&number); err != nil {
		return "", 0, err
	}
	s.event(ctx, p.ID, serverID, "info", fmt.Sprintf("Deployment #%d of %s started by %s", number, a.Name, o.Actor))
	if _, err = s.sendCommand(context.WithoutCancel(ctx), id, serverID, o.UserID, action, args, false); err != nil {
		s.db.Exec(context.WithoutCancel(ctx), `update deployments set status = 'failed', error = $2, finished_at = now() where id = $1`, id, err.Error())
		return "", 0, err
	}
	s.hub.publish(p.ID, "deployments", id)
	return id, number, nil
}

func (s *Server) deployApp(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var body struct {
		Image string `json:"image"`
	}
	if r.ContentLength > 0 {
		if err := decode(r, &body); err != nil {
			return err
		}
	}
	u := currentUser(r)
	var name string
	if err := s.db.QueryRow(r.Context(), `select name from applications where project_id = $1 and id::text = $2`, p.ID, r.PathValue("aid")).Scan(&name); err != nil {
		return err
	}
	s.auditProject(r, p, "app.deploy", name, map[string]any{"image": body.Image})
	id, number, err := s.startDeploy(r.Context(), p, r.PathValue("aid"), deployOpts{Image: body.Image, UserID: u.ID, Actor: u.label()})
	if err != nil {
		return err
	}
	writeJSON(w, 202, map[string]any{"id": id, "number": number})
	return nil
}

// promoteApp deploys this service's image to the same-named service in another project (e.g. staging → production).
func (s *Server) promoteApp(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var body struct {
		ProjectID string `json:"project_id"`
		Image     string `json:"image"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `select name from applications where project_id = $1 and id::text = $2`, p.ID, r.PathValue("aid")).Scan(&name); err != nil {
		return err
	}
	r.SetPathValue("pid", body.ProjectID)
	target, err := s.projectAccess(r, "member")
	if err != nil {
		return err
	}
	var targetApp string
	if err := s.db.QueryRow(r.Context(), `select id::text from applications where project_id = $1 and name = $2`, target.ID, name).Scan(&targetApp); err != nil {
		return errStatus(400, "the target project has no service named %s", name)
	}
	if strings.HasPrefix(body.Image, "orbit/") {
		return errStatus(400, "images built by Orbit live on one server; push to a registry to promote across projects")
	}
	u := currentUser(r)
	s.auditProject(r, target, "app.promote", name, map[string]any{"image": body.Image, "from_project": p.ID})
	id, number, err := s.startDeploy(r.Context(), target, targetApp, deployOpts{Image: body.Image, UserID: u.ID, Actor: u.label()})
	if err != nil {
		return err
	}
	writeJSON(w, 202, map[string]any{"id": id, "number": number, "project_id": target.ID})
	return nil
}

const deploymentsSQL = `select d.id, d.number, d.status, d.image, d.error, d.created_at, d.finished_at,
	extract(epoch from coalesce(d.finished_at, now()) - d.created_at)::int as duration,
	a.id as app_id, a.name as app, s.name as server_name, coalesce(u.email, case when d.commit_sha <> '' then 'github' end) as triggered_by,
	d.commit_sha, d.commit_message, d.commit_author, d.branch, a.orchestrator
	from deployments d join applications a on a.id = d.application_id
	left join servers s on s.id = d.server_id left join users u on u.id = d.triggered_by`

func (s *Server) listDeployments(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	q := r.URL.Query()
	out, err := s.list(r.Context(), deploymentsSQL+` where a.project_id = $1 and ($2 = '' or a.id::text = $2) and ($3 = '' or d.status = $3)
		order by d.created_at desc limit 100`, p.ID, q.Get("app"), q.Get("status"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) getDeployment(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.one(r.Context(), `select x.*, d.steps, d.log, d.server_id, d.plan as step_names,
		(select coalesce(json_agg(json_build_object('id', c.docker_id, 'name', c.name, 'state', c.state, 'status', c.status)), '[]')
		   from containers c where c.server_id = d.server_id and c.name like 'orbit-' || x.app || '-' || left(replace(d.id::text, '-', ''), 8)) as containers
		from (`+deploymentsSQL+`) x join deployments d on d.id = x.id
		where x.id::text = $2 and x.app_id in (select id from applications where project_id = $1)`, p.ID, r.PathValue("did"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

// listReleases lists each service's distinct successfully deployed images, newest first.
func (s *Server) listReleases(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `
		select * from (
		  select distinct on (a.id, d.image) a.id as app_id, a.name as app, d.image, d.number, d.commit_sha, d.commit_message, d.created_at,
		    d.image = (select image from deployments x where x.application_id = a.id and x.status = 'succeeded' order by number desc limit 1) as current
		  from deployments d join applications a on a.id = d.application_id
		  where a.project_id = $1 and d.status = 'succeeded' order by a.id, d.image, d.created_at desc
		) r order by app, created_at desc`, p.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) finishDeployment(ctx context.Context, ac *agentConn, res protocol.Result, status string) {
	var app, image string
	var number int
	var appID string
	err := s.db.QueryRow(ctx, `
		update deployments d set status = $3, error = nullif($4,''), finished_at = now()
		from applications a where a.id = d.application_id and d.id::text = $1 and d.server_id::text = $2 and d.status = 'running'
		returning a.name, d.image, d.number, a.id::text`, res.ID, ac.serverID, status, res.Error).Scan(&app, &image, &number, &appID)
	if err != nil {
		return // not a deployment
	}
	s.deploymentAlert(ctx, ac.projectID, appID, app, number, res.OK)
	if res.OK {
		s.event(ctx, ac.projectID, ac.serverID, "info", fmt.Sprintf("Deployment #%d of %s completed (%s)", number, app, image))
	} else {
		s.event(ctx, ac.projectID, ac.serverID, "error", fmt.Sprintf("Deployment #%d of %s failed: %s", number, app, res.Error))
	}
	s.hub.publish(ac.projectID, "deployments", res.ID)
	s.emit(ctx, ac.projectID, "deploy_finished", map[string]any{"service_id": appID, "service": app, "server_id": ac.serverID, "number": number, "status": status, "image": image, "error": res.Error})
}
