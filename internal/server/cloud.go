package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Managed infrastructure: Orbit creates servers in the customer's Hetzner Cloud account. Cloud-init installs
// Docker and the agent, which enrolls with a fresh token; the new server is linked to the provision by hostname.

var hetznerAPI = "https://api.hetzner.cloud/v1"

func hetzner(ctx context.Context, token, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, hetznerAPI+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct{ Message string }
		}
		json.Unmarshal(raw, &e)
		if e.Error.Message == "" {
			e.Error.Message = resp.Status
		}
		return fmt.Errorf("hetzner: %s", e.Error.Message)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (s *Server) listCloudAccounts(w http.ResponseWriter, r *http.Request) error {
	oid, _, err := s.orgAccess(r, "admin")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select id, provider, name, created_at from cloud_accounts where org_id::text = $1 order by created_at`, oid)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) createCloudAccount(w http.ResponseWriter, r *http.Request) error {
	oid, _, err := s.orgAccess(r, "admin")
	if err != nil {
		return err
	}
	var in struct {
		Name  string `json:"name"`
		Token string `json:"token"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if strings.TrimSpace(in.Name) == "" || in.Token == "" {
		return errStatus(400, "name and API token are required")
	}
	if err := hetzner(r.Context(), in.Token, "GET", "/locations", nil, nil); err != nil {
		return errStatus(400, "token check failed: %v", err)
	}
	out, err := s.one(r.Context(), `insert into cloud_accounts (org_id, provider, name, token_encrypted) values ($1,'hetzner',$2,$3) returning id`,
		oid, in.Name, s.encrypt([]byte(in.Token)))
	if err != nil {
		return err
	}
	u := currentUser(r)
	s.audit(r, oid, "", u.ID, u.label(), "cloud_account.create", in.Name, map[string]any{"provider": "hetzner"})
	writeJSON(w, 201, out)
	return nil
}

func (s *Server) deleteCloudAccount(w http.ResponseWriter, r *http.Request) error {
	oid, _, err := s.orgAccess(r, "admin")
	if err != nil {
		return err
	}
	var name string
	if err := s.db.QueryRow(r.Context(), `delete from cloud_accounts where org_id::text = $1 and id::text = $2 returning name`, oid, r.PathValue("id")).Scan(&name); err != nil {
		return err
	}
	u := currentUser(r)
	s.audit(r, oid, "", u.ID, u.label(), "cloud_account.delete", name, nil)
	w.WriteHeader(204)
	return nil
}

func (s *Server) cloudToken(ctx context.Context, orgID, accountID string) (string, error) {
	var enc []byte
	if err := s.db.QueryRow(ctx, `select token_encrypted from cloud_accounts where org_id::text = $1 and id::text = $2`, orgID, accountID).Scan(&enc); err != nil {
		return "", errStatus(400, "cloud account not found")
	}
	b, err := s.decrypt(enc)
	return string(b), err
}

func (s *Server) cloudOptions(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	token, err := s.cloudToken(r.Context(), p.OrgID, r.URL.Query().Get("account"))
	if err != nil {
		return err
	}
	var locs struct {
		Locations []struct {
			Name, City, Country, Description string
			NetworkZone                      string `json:"network_zone"`
		}
	}
	var types struct {
		ServerTypes []struct {
			Name, Description, Architecture string
			Cores                           int
			Memory                          float64
			Disk                            int
			Deprecated                      bool `json:"deprecated"`
			Prices                          []struct {
				Location     string
				PriceMonthly struct{ Gross string } `json:"price_monthly"`
			}
		} `json:"server_types"`
	}
	if err := hetzner(r.Context(), token, "GET", "/locations", nil, &locs); err != nil {
		return err
	}
	if err := hetzner(r.Context(), token, "GET", "/server_types?per_page=50", nil, &types); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"locations": locs.Locations, "server_types": types.ServerTypes})
	return nil
}

var hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// cloudInit installs Docker and the agent on first boot.
func cloudInit(appURL, token, region string) string {
	return fmt.Sprintf(`#cloud-config
runcmd:
  - curl -fsSL https://get.docker.com | sh
  - curl -fsSL %s/install.sh | ORBIT_TOKEN=%s ORBIT_REGION=%s sh
`, appURL, token, region)
}

func (s *Server) createProvision(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	var in struct {
		AccountID  string `json:"account_id"`
		Name       string `json:"name"`
		Location   string `json:"location"`
		ServerType string `json:"server_type"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if !hostnameRe.MatchString(in.Name) || !hostnameRe.MatchString(in.Location) || !hostnameRe.MatchString(in.ServerType) {
		return errStatus(400, "name, location and server type must be lowercase hostnames")
	}
	if !strings.HasPrefix(s.cfg.AppURL, "https://") || strings.Contains(s.cfg.AppURL, "localhost") {
		return errStatus(400, "APP_URL must be a public https URL so new servers can reach this control plane")
	}
	token, err := s.cloudToken(r.Context(), p.OrgID, in.AccountID)
	if err != nil {
		return err
	}
	enrollToken, hash := newToken("orb_enr_")
	if _, err := s.db.Exec(r.Context(), `insert into enrollment_tokens (project_id, token_hash, created_by, expires_at) values ($1,$2,$3, now() + interval '2 hours')`,
		p.ID, hash, currentUser(r).ID); err != nil {
		return err
	}
	var created struct {
		Server struct {
			ID        int64
			PublicNet struct {
				IPv4 struct{ IP string } `json:"ipv4"`
			} `json:"public_net"`
		}
	}
	if err := hetzner(r.Context(), token, "POST", "/servers", map[string]any{
		"name": in.Name, "server_type": in.ServerType, "location": in.Location, "image": "ubuntu-24.04",
		"user_data": cloudInit(s.cfg.AppURL, enrollToken, in.Location), "labels": map[string]string{"managed-by": "orbit"},
	}, &created); err != nil {
		s.db.Exec(context.WithoutCancel(r.Context()), `update enrollment_tokens set revoked_at = now() where token_hash = $1`, hash)
		return errStatus(400, "%v", err)
	}
	out, err := s.one(r.Context(), `insert into provisions (project_id, cloud_account_id, name, region, server_type, provider_id, ip, status, created_by)
		values ($1,$2,$3,$4,$5,$6,$7,'waiting_for_agent',$8) returning id`,
		p.ID, in.AccountID, in.Name, in.Location, in.ServerType, fmt.Sprint(created.Server.ID), created.Server.PublicNet.IPv4.IP, currentUser(r).ID)
	if err != nil {
		return err
	}
	s.auditProject(r, p, "server.provision", in.Name, map[string]any{"provider": "hetzner", "location": in.Location, "type": in.ServerType})
	s.event(r.Context(), p.ID, "", "info", fmt.Sprintf("Provisioning %s (%s, %s) on Hetzner Cloud", in.Name, in.ServerType, in.Location))
	writeJSON(w, 202, out)
	return nil
}

func (s *Server) listProvisions(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "viewer")
	if err != nil {
		return err
	}
	out, err := s.list(r.Context(), `select v.id, v.name, v.region, v.server_type, v.provider_id, v.ip, v.status, v.error, v.server_id, v.created_at, a.name as account
		from provisions v left join cloud_accounts a on a.id = v.cloud_account_id
		where v.project_id = $1 and v.status <> 'deleted' order by v.created_at desc limit 50`, p.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

// linkProvision attaches a freshly enrolled server to the provision that created it.
func (s *Server) linkProvision(ctx context.Context, projectID, serverID, hostname string) {
	var providerID string
	if s.db.QueryRow(ctx, `update provisions set server_id = $3, status = 'ready' where project_id::text = $1 and name = $2 and status = 'waiting_for_agent'
		returning provider_id`, projectID, hostname, serverID).Scan(&providerID) == nil {
		s.db.Exec(ctx, `update servers set provider = 'hetzner', provider_id = $2, region = case when region = '' then (select region from provisions where server_id = $1 limit 1) else region end where id = $1`, serverID, providerID)
	}
}

func (s *Server) expireProvisions(ctx context.Context) {
	s.db.Exec(ctx, `update provisions set status = 'failed', error = 'the agent did not connect within 30 minutes (check cloud-init logs: /var/log/cloud-init-output.log)'
		where status = 'waiting_for_agent' and created_at < now() - interval '30 minutes'`)
}

// destroyServer deletes the cloud server behind a provisioned server (or a failed provision), then forgets it.
func (s *Server) destroyProvision(w http.ResponseWriter, r *http.Request) error {
	p, err := s.projectAccess(r, "admin")
	if err != nil {
		return err
	}
	var name, providerID, accountID string
	var serverID *string
	err = s.db.QueryRow(r.Context(), `select name, provider_id, coalesce(cloud_account_id::text, ''), server_id::text from provisions
		where project_id = $1 and id::text = $2 and status <> 'deleted'`, p.ID, r.PathValue("id")).Scan(&name, &providerID, &accountID, &serverID)
	if err == pgx.ErrNoRows {
		return errStatus(404, "provision not found")
	} else if err != nil {
		return err
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := decode(r, &body); err != nil || body.Confirm != name {
		return errStatus(400, "type the server name to confirm")
	}
	token, err := s.cloudToken(r.Context(), p.OrgID, accountID)
	if err != nil {
		return err
	}
	if providerID != "" {
		if err := hetzner(r.Context(), token, "DELETE", "/servers/"+providerID, nil, nil); err != nil && !strings.Contains(err.Error(), "not found") {
			return errStatus(502, "%v", err)
		}
	}
	s.db.Exec(r.Context(), `update provisions set status = 'deleted' where id::text = $1`, r.PathValue("id"))
	if serverID != nil {
		s.db.Exec(r.Context(), `delete from servers where id::text = $1`, *serverID)
		s.hub.mu.Lock()
		if ac := s.hub.agents[*serverID]; ac != nil {
			ac.ws.CloseNow()
		}
		s.hub.mu.Unlock()
	}
	s.auditProject(r, p, "server.destroy", name, map[string]any{"provider": "hetzner", "provider_id": providerID})
	s.event(r.Context(), p.ID, "", "warning", fmt.Sprintf("Destroyed cloud server %s", name))
	s.hub.publish(p.ID, "servers", "")
	w.WriteHeader(204)
	return nil
}
