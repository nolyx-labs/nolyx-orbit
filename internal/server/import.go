// Importing an existing container as a service: the agent reports the container's settings, a service is created
// from them, and its first deploy replaces the container, keeping the original stopped as a fallback.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"orbit/internal/protocol"
)

func (s *Server) importContainer(w http.ResponseWriter, r *http.Request) error {
	// Admin: an import can carry host bind mounts into a service, which editing a service never allows.
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	if _, err := s.containerTarget(r, p); err != nil {
		return err
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if !appNameRe.MatchString(in.Name) {
		return errStatus(400, "name must be lowercase letters, digits and dashes")
	}
	sid, cid, u := r.PathValue("sid"), r.PathValue("cid"), currentUser(r)
	res, err := s.sendCommand(r.Context(), newUUID(), sid, u.ID, protocol.ActionContainerInspect, protocol.ContainerArgs{ContainerID: cid}, true)
	if err != nil {
		return err
	}
	if !res.OK {
		return errStatus(502, "agent: %s", res.Error)
	}
	var spec protocol.ImportSpec
	if err := json.Unmarshal(res.Output, &spec); err != nil {
		return err
	}
	if len(spec.Unsupported) > 0 {
		return errStatus(400, "Orbit can't import this container because it %s", strings.Join(spec.Unsupported, ", "))
	}
	if err := (appInput{Ports: spec.Ports, Env: spec.Env, Command: spec.Command}).validate(); err != nil {
		return err
	}
	envJSON, _ := json.Marshal(spec.Env)
	var id string
	err = s.db.QueryRow(r.Context(), `insert into applications (project_id, server_id, name, image, ports, env_encrypted, env_keys, command,
		carried, import_container, webhook_secret) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) on conflict do nothing returning id::text`,
		p.ID, sid, in.Name, spec.Image, mustJSON(spec.Ports), s.encrypt(envJSON), sortedKeys(spec.Env), mustJSON(spec.Command),
		mustJSON(spec.Carry), cid, randomSecret()).Scan(&id)
	if err == pgx.ErrNoRows {
		return errStatus(409, "a service named %s already exists", in.Name)
	} else if err != nil {
		return err
	}
	s.auditProject(r, p, "app.import", in.Name, map[string]any{"container": spec.Name, "server_id": sid, "image": spec.Image,
		"mounts": spec.Carry.Mounts, "compose": spec.Compose})
	dep, number, err := s.startDeploy(r.Context(), p, id, deployOpts{UserID: u.ID, Actor: u.label()})
	if err != nil {
		s.db.Exec(r.Context(), `delete from applications where id::text = $1`, id)
		return err
	}
	writeJSON(w, 201, map[string]any{"id": id, "deployment_id": dep, "number": number})
	return nil
}

// guardHostMounts: a service that kept host bind mounts from an import can reach the server's files, so only admins
// may change its settings (image "") or deploy an image it hasn't run before.
func (s *Server) guardHostMounts(ctx context.Context, p project, appID, image string) error {
	if roleRank[p.Role] >= roleRank["admin"] {
		return nil
	}
	var risky bool
	err := s.db.QueryRow(ctx, `select exists(select 1 from applications a, jsonb_array_elements(coalesce(a.carried->'mounts', '[]')) m
		  where a.id::text = $1 and m->>'type' = 'bind')
		and ($2 = '' or not exists(select 1 from deployments where application_id::text = $1 and image = $2))`, appID, image).Scan(&risky)
	if err != nil {
		return err
	}
	if risky {
		return errStatus(403, "this service mounts folders from its server, so only admins can change it or deploy a new image")
	}
	return nil
}
