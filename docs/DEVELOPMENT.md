# Development

## Run locally

```bash
docker run -d --name orbit-pg -e POSTGRES_USER=orbit -e POSTGRES_PASSWORD=orbit -p 5432:5432 postgres:17-alpine
DATABASE_URL=postgres://orbit:orbit@localhost:5432/orbit?sslmode=disable ENCRYPTION_KEY=$(openssl rand -hex 32) go run ./cmd/orbit
cd web && npm install && npm run dev        # Vite on :5173, proxies /api to :8080
```

## Test and format

```bash
go test ./...                               # database tests also need TEST_DATABASE_URL
cd web && npm run typecheck && npm test
cd web && npm run format
```

## Code layout

**Backend.** Each Go package has one file per area.

| Path | What |
|---|---|
| `cmd/orbit`, `cmd/orbit-agent` | Entry points for the control plane and the agent |
| `internal/protocol` | Messages exchanged by the agent and the control plane |
| `internal/uptime` | HTTP and TCP checks, shared by the control plane and agents |
| `internal/server` | Control plane |
| `internal/agent` | Agent |

Areas in `internal/server`:

- **Core:** `server.go` (setup and migrations), `http.go`, `routes.go`, `crypto.go`, `worker.go`
- **Access:** `auth.go`, `sessions.go`, `members.go`, `teams.go`, `roles.go`, `apikeys.go`, `audit.go`, `oidc.go`
- **Agents:** `hub.go`, `agent_session.go`, `commands.go`, `enrollment.go`
- **Infrastructure:** `servers.go`, `containers.go`, `metrics.go`, `overview.go`, `insight.go`, `maintenance.go`
- **Services:** `apps.go`, `deployments.go`, `github.go`, `import.go`, `catalog.go`
- **Databases:** `databases.go`, `database_monitoring.go`, `replicas.go`, `backups.go`, `operations.go`
- **Observability:** `alerts.go`, `alert_rules.go`, `notifications.go`, `logs.go`, `log_forwarding.go`, `monitors.go`, `statuspages.go`
- **Other:** `secrets.go`, `nomad.go`, `cloud.go`, `workflows.go`

Areas in `internal/agent`:

- **Connection:** `agent.go` (connection lifecycle), `commands.go`, `actionlog.go`
- **Host:** `collect.go`, `host.go`, `insight.go`, `systemd.go`, `journal.go` (service logs), `monitors.go` (checks as a monitor location)
- **Docker:** `docker.go`, `docker_ops.go`, `deploy.go`, `import.go`, `proxy.go`, `logs.go`
- **PostgreSQL:** `postgres_monitor.go`, `postgres_manage.go`, `postgres_backup.go`
- **Nomad:** `nomad.go`, `nomad_install.go`

**Frontend (`web/src`).** Each page area has its own folder. Dialogs and tabs live in their own files.

| Path | What |
|---|---|
| `main.tsx` | Routes |
| `lib/` | `api.ts` (HTTP and data hooks), `live.ts` (live updates), `types.ts`, `format.ts` |
| `components/layout/` | Shell, sidebar, project context, theme |
| `components/ui/` | Shared building blocks: `PageHead`, `Panel`, `status`, `Dialog`, `Confirm`, `controls` |
| `components/charts/`, `components/containers/` | Charts, and the container table and dialogs |
| `pages/<area>/` | One folder per area, such as `servers`, `services`, `databases`, `observability`, `monitors`, `security` and `settings` |

For how the pieces fit together, see [ARCHITECTURE.md](ARCHITECTURE.md).
