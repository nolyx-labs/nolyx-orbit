package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

const sessionCookie = "orbit_session"

type ctxKey int

const userKey ctxKey = 0

// user is the request principal: a signed-in user, or an API key acting on behalf of its creator.
type user struct {
	ID, Email, Name string
	Key             *apiKey
}

type apiKey struct {
	ID, Name, OrgID, ProjectID, Role string
}

func currentUser(r *http.Request) user { return r.Context().Value(userKey).(user) }

// label is how the principal appears in the audit log.
func (u user) label() string {
	if u.Key != nil {
		return "api-key:" + u.Key.Name + " (" + u.Email + ")"
	}
	return u.Email
}

var roleRank = map[string]int{"viewer": 0, "member": 1, "admin": 2, "owner": 3}
var rankRole = []string{"viewer", "member", "admin", "owner"}

type project struct {
	ID, OrgID, Role string
}

// projectAccess resolves {pid} and enforces access with at least minRole. This is the tenancy boundary.
// Effective role = max(organization role, team grants on this project); API keys never exceed their own role.
func (s *Server) projectAccess(r *http.Request, minRole string) (project, error) {
	p := project{ID: r.PathValue("pid")}
	u := currentUser(r)
	var teamRank int
	err := s.db.QueryRow(r.Context(), `
		select p.org_id::text, m.role, coalesce((
		  select max(case tpr.role when 'viewer' then 0 when 'member' then 1 when 'admin' then 2 end)
		  from team_members tm join team_project_roles tpr on tpr.team_id = tm.team_id
		  where tm.user_id = m.user_id and tpr.project_id = p.id), -1)
		from projects p
		join memberships m on m.org_id = p.org_id and m.user_id = $2
		where p.id::text = $1`, p.ID, u.ID).Scan(&p.OrgID, &p.Role, &teamRank)
	if err == pgx.ErrNoRows {
		return p, errStatus(404, "project not found")
	}
	if err != nil {
		return p, err
	}
	if teamRank > roleRank[p.Role] {
		p.Role = rankRole[teamRank]
	}
	if k := u.Key; k != nil {
		if k.OrgID != p.OrgID || (k.ProjectID != "" && k.ProjectID != p.ID) {
			return p, errStatus(404, "project not found")
		}
		p.Role = rankRole[min(roleRank[k.Role], roleRank[p.Role])]
	}
	if roleRank[p.Role] < roleRank[minRole] {
		return p, errStatus(403, "requires %s role", minRole)
	}
	return p, nil
}

// orgAccess enforces organization membership for {oid} with at least minRole.
func (s *Server) orgAccess(r *http.Request, minRole string) (string, string, error) {
	oid, u := r.PathValue("oid"), currentUser(r)
	var role string
	err := s.db.QueryRow(r.Context(), `select role from memberships where org_id::text = $1 and user_id = $2`, oid, u.ID).Scan(&role)
	if err == pgx.ErrNoRows {
		return "", "", errStatus(404, "organization not found")
	} else if err != nil {
		return "", "", err
	}
	if k := u.Key; k != nil {
		if k.OrgID != oid || k.ProjectID != "" {
			return "", "", errStatus(403, "API key is not scoped to this organization")
		}
		role = rankRole[min(roleRank[k.Role], roleRank[role])]
	}
	if roleRank[role] < roleRank[minRole] {
		return "", "", errStatus(403, "requires %s role", minRole)
	}
	return oid, role, nil
}

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var u user
		var err error
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			u, err = s.keyPrincipal(r.Context(), bearer)
		} else {
			var c *http.Cookie
			if c, err = r.Cookie(sessionCookie); err == nil {
				err = s.db.QueryRow(r.Context(), `
					select u.id::text, u.email, u.name from sessions s join users u on u.id = s.user_id
					where s.token_hash = $1 and s.expires_at > now()`, hashToken(c.Value)).Scan(&u.ID, &u.Email, &u.Name)
			}
			// CSRF: SameSite=Lax blocks cross-site cookie POSTs; mutations must also be JSON (decode enforces it)
			// or carry the header below, which a cross-site form cannot set. Bearer keys aren't ambient: exempt.
			if err == nil && r.Method != "GET" && r.Header.Get("X-Orbit-Request") != "1" {
				writeJSON(w, 403, map[string]string{"error": "missing X-Orbit-Request header"})
				return
			}
		}
		if err != nil {
			writeJSON(w, 401, map[string]string{"error": "not signed in"})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	})
}

func (s *Server) keyPrincipal(ctx context.Context, token string) (user, error) {
	var u user
	k := &apiKey{}
	err := s.db.QueryRow(ctx, `
		update api_keys k set last_used_at = now() from users u
		where k.key_hash = $1 and k.revoked_at is null and (k.expires_at is null or k.expires_at > now()) and u.id = k.created_by
		returning k.id::text, k.name, k.org_id::text, coalesce(k.project_id::text, ''), k.role, u.id::text, u.email, u.name`,
		hashToken(token)).Scan(&k.ID, &k.Name, &k.OrgID, &k.ProjectID, &k.Role, &u.ID, &u.Email, &u.Name)
	u.Key = k
	return u, err
}
