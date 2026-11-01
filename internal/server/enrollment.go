// Enrollment tokens and agent enrollment.
package server

import (
	"crypto/ed25519"
	"fmt"
	"net/http"
	"orbit/internal/protocol"

	"github.com/jackc/pgx/v5"
)

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select t.id, t.expires_at, t.last_used_at, t.created_at, u.email as created_by
		from enrollment_tokens t left join users u on u.id = t.created_by
		where t.project_id = $1 and t.revoked_at is null and t.expires_at > now() order by t.created_at desc`, p.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	token, hash := newToken("orb_enr_")
	var id string
	if err := s.db.QueryRow(r.Context(), `insert into enrollment_tokens (project_id, token_hash, created_by, expires_at)
		values ($1,$2,$3, now() + interval '24 hours') returning id::text`, p.ID, hash, currentUser(r).ID).Scan(&id); err != nil {
		return err
	}
	s.auditProject(r, p, "enrollment_token.create", id, nil)
	writeJSON(w, 201, map[string]string{"id": id, "token": token, "app_url": s.cfg.AppURL})
	return nil
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(r.Context(), `update enrollment_tokens set revoked_at = now() where project_id = $1 and id::text = $2`, p.ID, r.PathValue("id"))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	s.auditProject(r, p, "enrollment_token.revoke", r.PathValue("id"), nil)
	w.WriteHeader(204)
	return nil
}

func (s *Server) enroll(w http.ResponseWriter, r *http.Request) error {
	var req protocol.EnrollRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	var tokenID, projectID string
	err := s.db.QueryRow(r.Context(), `
		update enrollment_tokens set last_used_at = now()
		where token_hash = $1 and revoked_at is null and expires_at > now()
		returning id::text, project_id::text`, hashToken(req.Token)).Scan(&tokenID, &projectID)
	if err == pgx.ErrNoRows {
		return errStatus(401, "invalid or expired enrollment token")
	} else if err != nil {
		return err
	}
	secret, hash := newToken("orb_agt_")
	hl := req.Host
	name := hl.Hostname
	if name == "" {
		name = "server"
	}
	var serverID, orgID string
	err = s.db.QueryRow(r.Context(), `
		insert into servers (project_id, name, hostname, ip, os, arch, kernel, cpu_count, mem_total, disk_total, agent_version, docker_version, agent_secret_hash, region)
		values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		returning id::text, (select org_id::text from projects where id = $1)`,
		projectID, trunc(name, 64), trunc(hl.Hostname, 255), trunc(hl.IP, 64), trunc(hl.OS, 128), trunc(hl.Arch, 32), trunc(hl.Kernel, 128),
		hl.CPUCount, int64(hl.MemTotal), int64(hl.DiskTotal), trunc(hl.AgentVersion, 32), trunc(hl.DockerVersion, 32), hash, trunc(hl.Region, 64)).Scan(&serverID, &orgID)
	if err != nil {
		return err
	}
	s.audit(r, orgID, projectID, "", "agent:"+serverID, "server.enroll", name, map[string]any{"token_id": tokenID})
	s.event(r.Context(), projectID, serverID, "info", fmt.Sprintf("Server %s enrolled", name))
	s.hub.publish(projectID, "servers", serverID)
	s.linkProvision(r.Context(), projectID, serverID, hl.Hostname)
	writeJSON(w, 200, protocol.EnrollResponse{ServerID: serverID, Secret: secret, SigningKey: s.signer.Public().(ed25519.PublicKey)})
	return nil
}
