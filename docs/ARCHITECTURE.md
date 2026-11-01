# Nolyx Orbit — Architecture

> Infrastructure management without the Kubernetes complexity.

Orbit is a **self-hosted** control plane. You run it with `docker compose up -d`, then connect your servers to it by installing a small Go agent on each one. The agent only ever dials **out** to the control plane, so your servers need no inbound ports.

## 1. Components

```
                ┌──────────────────────────────────────────────┐
 Browser ──────▶│ orbit (single Go binary)                     │
  SPA + SSE     │  ├─ /            embedded React SPA          │
                │  ├─ /api/v1      JSON API (cookie or API key)│
                │  ├─ /api/v1/…/stream   SSE realtime          │
                │  ├─ /agent/v1    enroll + WebSocket gateway  │
                │  ├─ /hooks/github/{app}  push-to-deploy      │
                │  ├─ hub          connected agents, signed cmds│
                │  └─ worker       alerts, backups, retention, │
                │                  offline detection           │
                └───────────────┬──────────────────────────────┘
                                │ PostgreSQL (app data, metrics, logs)
        wss:// outbound only    │
   ┌────────────┬───────────────┴┬────────────┐
 agent        agent            agent        agent
   │ docker.sock · /proc · PostgreSQL connections · local Nomad API · HTTP proxy (zero-downtime apps)
```

**Deliberate choices:**

- **No Redis and no separate worker or realtime services.** They all run as goroutines in the single binary. See §7 for how this scales out.
- **Metrics and logs live in Postgres.** Metrics are kept for 30 days and bucketed with `date_bin`. Logs are kept for 7 days with a `pg_trgm` search index. TimescaleDB or ClickHouse can replace this when volume requires it.
- **The UI is a Vite SPA embedded in the Go binary.** There's no Next.js.

## 2. Data model

The migrations in [`internal/server/migrations/`](../internal/server/migrations/) are the source of truth.

```
organizations ─┬─ memberships ── users ── sessions
               ├─ invites, teams ── team_members, team_project_roles
               ├─ api_keys, secrets (org-wide), cloud_accounts
               ├─ projects ─┬─ enrollment_tokens, secrets (project)
               │            ├─ servers ─┬─ server_metrics, containers ── container_metrics
               │            │           ├─ commands (ledger of every signed command)
               │            │           └─ log_lines
               │            ├─ applications ─┬─ deployments
               │            │                └─ app_metrics (proxy request metrics)
               │            ├─ databases ─┬─ database_metrics, backups, backup_configs
               │            │             └─ primary_id (replica topology)
               │            ├─ nomad_clusters (snapshots reported by agents)
               │            ├─ operations (provision, replica, backup, restore, promote, Nomad install)
               │            ├─ provisions (servers created in Hetzner Cloud)
               │            ├─ events, notification_channels, alert_rules ── alert_incidents
               └─ audit_events
```

- **Tenancy:** every handler resolves project → org → membership before it touches data.
  - The effective role is the higher of your organization role and any team grant on that project.
  - An API key's role is capped by the role of the user who created it.
- **Encryption at rest:** AES-256-GCM, using `ENCRYPTION_KEY`, for:
  - app environment variables and secrets
  - database and replication passwords
  - S3 credentials
  - notification channel URLs
  - Git access tokens
  - cloud API tokens
- **Hashed credentials:** session, enrollment, agent, API-key and invite tokens are stored only as SHA-256 hashes.

## 3. Agent ↔ control plane protocol (v1)

The wire types are in [`internal/protocol`](../internal/protocol/protocol.go).

**Enrollment** happens once:

- The agent posts to `POST /agent/v1/enroll`.
- It gets back `{server_id, secret, signing_key}` and stores them at mode `0600`.

**Session:**

- The agent opens a WebSocket at `/agent/v1/connect`, authenticated with `Bearer server_id:secret`.
- Frames are JSON envelopes.

| Direction | type | Purpose |
|---|---|---|
| agent → cp | `hello` | Host facts, plus Docker and Nomad versions and region |
| agent → cp | `metrics` | Every 10s, including disk I/O. Doubles as the heartbeat. 1h is buffered while offline. |
| agent → cp | `containers` | Every 15s, with per-container CPU and memory |
| agent → cp | `logs` | Every 3s, using Docker's `since` cursor. Cursors only advance after delivery. |
| agent → cp | `db_stats` | Every 15s per monitored database: connections, TPS/QPS, cache hit, locks, slow queries, tables, replication |
| agent → cp | `app_metrics` | Every 10s: request counts, 5xx and latency from the zero-downtime proxy |
| agent → cp | `nomad` | Every 30s when a Nomad agent is reachable: nodes, jobs, allocations, deployments |
| cp → agent | `config` | Databases to monitor, with credentials. Sent on connect and whenever they change. |
| cp → agent | `command` | `{id, action, args, expires_at, signature}` |
| agent → cp | `progress` / `result` | Step updates and the final outcome for deployments and operations |

**Command security:**

- **Before sending,** the control plane checks RBAC, writes the command to the ledger, and writes the audit log.
- **The signature** is ed25519 over `id | server_id | action | expires_at | sha256(args)`.
  - The signing key is derived from `ENCRYPTION_KEY` with HKDF.
  - The agent pins the public key at enrollment.
- **The agent rejects** any command that:
  - has an unknown action,
  - has a bad signature,
  - was signed for a different server,
  - is past its expiry, or
  - reuses an ID it has seen before.

**Actions:**

- `container.{start,stop,restart,remove,logs}`
- `deploy`
- `db.{provision,replica.create,resync,remove,promote,backup,restore}`
- `nomad.{register,stop,install}`

There is no shell or terminal action.

## 4. Features and how they work

- **Deploys** have three modes:
  - **Direct** (default): the agent stops the old container, starts the new one, and runs a health check. If the check fails, it restarts the old container.
  - **Zero-downtime** (opt-in, HTTP): the agent owns the first host port and runs an HTTP reverse proxy on it. It starts the new container on the bridge network, health-checks it, and switches traffic atomically. Then it drains the old container for 5 seconds and removes it.
    - The proxy also records request rate, error rate and p95 latency.
    - Proxy routes are rebuilt from container labels when the agent restarts.
  - **Nomad:** the control plane translates the service into a Nomad job:
    - static ports with `distinct_hosts`
    - Nomad-native HTTP checks
    - rolling updates with auto-revert
    - a spread across datacenters

    The agent submits the job and follows the rollout, failing fast if Nomad can't place it.
- **Git:** the Docker daemon builds the image from a remote context, `repo.git#<sha>:<context>`.
  - The commit is resolved through the GitHub API.
  - A GitHub push webhook, verified with a per-service HMAC secret, deploys the configured branch.
  - Built images stay local to the server that built them, so Nomad services need a registry image.
- **Secrets** are merged in this order, with later values winning: organization → project → service. Values are write-only.
- **PostgreSQL:**
  - **Monitoring:** the agent connects directly to each database.
  - **Topology:** databases are grouped by their Postgres system identifier.
  - **Managed instances:** these are official `postgres` containers with replication enabled and `pg_stat_statements` loaded.
  - **Replicas** are cloned with `pg_basebackup -R` and run as hot standbys.
  - **Promote** runs `pg_promote()`. **Re-sync** wipes the replica and clones it again.
  - **Backups:**
    - `pg_dump -Fc` writes to the `orbit-backups` volume on the database's server, with optional upload to S3.
    - Schedules and retention are configurable.
    - Restores run `pg_restore --clean`.
- **Alerts** come in two kinds:
  - **Threshold alerts** on server CPU, memory, disk, disk busy and offline, database replication lag, connections and down, and service error rate.
  - **Event alerts** for unhealthy containers and failed deployments.

  They notify through email (SMTP), Slack, Discord or a webhook. Incidents can be acknowledged. Outbound webhooks block private addresses unless `ALLOW_PRIVATE_WEBHOOKS=true`.
- **Teams:**
  - Invites are bound to the invited email address.
  - Teams can raise a member's role on specific projects.
- **Regions:** each server has a region label, set from the agent's `ORBIT_REGION` or edited in the UI. You can filter servers by region, and Nomad jobs spread across datacenters.
- **Managed servers (Hetzner Cloud):**
  - Orbit creates the server with cloud-init, which installs Docker and the agent with a short-lived token.
  - The new server is linked to its provision by hostname.
  - Destroying it deletes the cloud server too.
  - This requires a public https `APP_URL`.

## 5. Known limits

- **Private registries:** image pulls don't support them yet. Git builds use a token instead.
- **Zero-downtime proxy:** HTTP only, and it doesn't terminate TLS. Put a load balancer in front for HTTPS. The agent is in the request path for these services.
- **Nomad installs:** they run the official image with `--cgroupns=host`. HashiCorp doesn't officially support running Nomad clients in Docker, and this setup supports the Docker task driver only.
- **Database engines:** PostgreSQL only. The protocol and schema leave room for MySQL and MariaDB.
- **Promotion:** it doesn't fence the old primary. The UI warns about split brain and shows both primaries.
- **Host logs:** journald isn't collected. Container logs are, including those from managed databases.

## 6. Configuration

All configuration is environment variables. See [`.env.example`](../.env.example). Only these are required:

- `ENCRYPTION_KEY`
- `DATABASE_URL`, which Compose sets for you

## 7. Scaling the control plane (path, not built)

Agents hold long-lived WebSockets, so any instance can terminate any agent. To scale out:

1. Each instance records `server_id → instance_id` in Postgres.
2. API instances publish commands over Postgres `LISTEN`/`NOTIFY` or Redis. The instance that owns that connection forwards the command. Results come back the same way.
3. SSE fan-out uses the same channel.

Commands already carry their ID, expiry and signature, so routing doesn't need any protocol change.
