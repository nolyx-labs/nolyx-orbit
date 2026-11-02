# Nolyx Orbit

**Self-hosted infrastructure management without the Kubernetes complexity.**

Nolyx Orbit is a control plane that you run on your own server. Install a small agent on each machine you want to manage. The agent connects **outbound** to Orbit, so those machines need no inbound ports.

What you can do with it:

- **Deploy:** run Docker services with zero-downtime deploys, Git push-to-deploy, releases and rollbacks, or import containers and Compose services that are already running.
- **Orchestrate:** run services across a Nomad cluster, or on a single server without Nomad.
- **Databases:** monitor PostgreSQL, add read replicas, promote replicas, and schedule backups (including to S3).
- **Observe:** metrics, searchable central logs from containers and system services (optionally copied into your own PostgreSQL), events, uptime monitors checked from several locations, and alerts sent by email, Slack, Discord or webhook.
- **Status pages:** public pages in the style of Atlassian Statuspage, with 90-day uptime, incidents, scheduled maintenance, email and webhook subscribers, feeds and custom domains.
- **Secure:** secrets, API keys, teams with per-project roles, custom roles, and an audit log of every action.
- **Provision:** create new servers in Hetzner Cloud with the agent pre-installed.

---

## Contents

1. [Requirements](#requirements)
2. [Install](#install)
3. [Choose how you'll reach it](#choose-how-youll-reach-it)
   - [Option A: a domain with HTTPS (recommended)](#option-a-a-domain-with-https-recommended)
   - [Option B: plain HTTP](#option-b-plain-http)
   - [Option C: your own reverse proxy](#option-c-your-own-reverse-proxy)
4. [Connect servers](#connect-servers)
   - [How the agent connection is secured](#how-the-agent-connection-is-secured)
5. [Updating, backups and data](#updating-backups-and-data)
6. [Configuration reference](#configuration-reference)
7. [Troubleshooting](#troubleshooting)
8. [Development](#development)

---

## Requirements

**Control plane**
- A Linux server with Docker Engine and the Docker Compose plugin.
- At least 1 vCPU and 1 GB of RAM. 2 GB is better once you have many servers or logs.

**Managed servers**
- Linux (amd64 or arm64) with systemd, or Docker if you run the agent as a container.
- They only need to reach the control plane's URL. No inbound ports.

---

## Install

On the machine that will run the control plane:

```bash
git clone <this repository> nolyx-orbit && cd nolyx-orbit
cp .env.example .env
sed -i.bak "s/^ENCRYPTION_KEY=.*/ENCRYPTION_KEY=$(openssl rand -hex 32)/" .env && rm .env.bak
```

**Back up `.env` now.** `ENCRYPTION_KEY` encrypts every stored secret and signs every command sent to agents. If you lose it, your secrets become unreadable and every agent has to re-enroll.

Next, decide how people will reach Orbit (below) and set `APP_URL`. Then start it:

```bash
docker compose up -d --build
```

Open `APP_URL/signup` and create the first account. The first account becomes the owner. After that, sign-up is closed: add everyone else from **Security → Access → Invite**. Invited people can create an account from their invitation link.

Turn on two-factor authentication for your account in **Settings → Account**.

---

## Choose how you'll reach it

`APP_URL` is the address that browsers **and agents** use. Orbit uses it in:

- install commands
- invite links
- GitHub webhook URLs

When it starts with `https://`, login cookies are also marked Secure. Set it before you connect servers. Changing it later means updating every agent (see [Troubleshooting](#troubleshooting)).

### Option A: a domain with HTTPS (recommended)

Orbit ships with an optional [Caddy](https://caddyserver.com) proxy. Caddy gets a free Let's Encrypt certificate and renews it automatically.

1. **Point a domain at the server.**
   - Create an `A` record, and an `AAAA` record if you use IPv6, for example `orbit.example.com` → your server's public IP.
   - Wait until `dig +short orbit.example.com` returns that IP.

2. **Open the firewall** for ports `80` and `443` (TCP, plus UDP 443 for HTTP/3).
   - Let's Encrypt validates the domain over port 80, so port 80 has to stay open.

3. **Set these in `.env`:**

   ```env
   APP_URL=https://orbit.example.com
   ORBIT_DOMAIN=orbit.example.com
   ORBIT_BIND=127.0.0.1      # keep port 8080 private; only Caddy talks to it
   ```

4. **Start with the `https` profile:**

   ```bash
   docker compose --profile https up -d --build
   ```

Open https://orbit.example.com. The first request can take a few seconds while the certificate is issued. If it fails, run `docker compose logs caddy`; the usual cause is DNS or port 80.

Always include `--profile https` when you run `up`, so Caddy keeps running. Certificates are stored in the `nolyx-orbit-caddy` volume and survive restarts.

Status pages can also have their own domain, such as `status.example.com`. Point a `CNAME` for it at `ORBIT_DOMAIN`, then enter it in the status page's **Settings → Custom domain**. Caddy issues its certificate on the first visit (see [Caddyfile](Caddyfile)). With your own proxy (Option C), route those domains to Orbit with their `Host` header intact.

### Option B: plain HTTP

Use this on a private network, over a VPN such as Tailscale or WireGuard, or for a quick trial.

**Warning:** over HTTP, passwords, session cookies and agent credentials travel unencrypted. Agent commands are still signed, so they can't be forged, but anyone on the network path can read the traffic. Don't use plain HTTP over the public internet.

1. Set `APP_URL` in `.env` to the address your browser and servers will use:

   ```env
   APP_URL=http://192.168.1.20:8080       # or http://orbit.internal:8080
   ```

2. Open port `8080` to the machines that need it, and nothing else.

3. Start Orbit:

   ```bash
   docker compose up -d --build
   ```

Agents refuse to connect over plain HTTP unless you allow it explicitly with `ORBIT_INSECURE=1`. When `APP_URL` is `http://`, the install command on the **Agents** page adds this automatically and shows a warning.

### Option C: your own reverse proxy

If you already run nginx, Traefik or a load balancer, proxy to Orbit on port 8080:

- Set `APP_URL=https://your-domain` and `ORBIT_BIND=127.0.0.1` (when the proxy runs on the same host).
- Don't use the `https` profile.
- Make sure the proxy passes through the agent WebSocket (`/agent/v1/connect`) and doesn't buffer the live-update stream (`/api/v1/projects/*/stream`).

nginx example:

```nginx
server {
    listen 443 ssl http2;
    server_name orbit.example.com;
    # ssl_certificate / ssl_certificate_key …

    client_max_body_size 10m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;      # agent WebSocket
        proxy_set_header Connection "upgrade";
        proxy_buffering off;                         # live updates (SSE)
        proxy_read_timeout 1h;
    }
}
```

---

## Connect servers

In Orbit, open **Settings → Agents → Generate enrollment token**. A token works for 24 hours and can enroll several servers.

**Any Linux server (systemd):** run the command shown on the page. It looks like this:

```bash
curl -fsSL https://orbit.example.com/install.sh | sudo ORBIT_TOKEN=orb_enr_… ORBIT_REGION=eu-central sh
```

- The agent is downloaded from your own control plane and installed as the `orbit-agent` service.
- Connecting is read-only. The agent reports what the server runs and scans it (`apt-get -s`, `dnf --cacheonly`), and changes nothing until someone acts in Orbit.
- `ORBIT_REGION` is optional and labels the server.
- To follow its logs, run `journalctl -u orbit-agent -f`.
- To remove the agent, run `sudo orbit-agent disconnect`. It removes the service, its config and the server's identity, and leaves every container and service running.
- Every command the agent runs is recorded on the server in `/var/lib/orbit-agent/actions.log`, one JSON line each: time, command ID, action, result and a SHA-256 of its arguments (never the arguments, which can hold secrets). It rotates to `actions.log.1` at 10 MB.

**The control-plane machine itself:**

```bash
ORBIT_TOKEN=orb_enr_… docker compose --profile agent up -d --build agent
```

**Create a new server in Hetzner Cloud:**

1. Add a Hetzner API token in **Settings → Organization**.
2. Use **Settings → Agents → Create server**. This requires an `https://` `APP_URL`.

Servers appear under **Servers** within seconds. Containers, databases and logs follow shortly after.

### How the agent connection is secured

- **Outbound only.** The agent dials the control plane over HTTPS (a WebSocket to `ORBIT_URL`, port 443 by default). The server needs no inbound ports.
- **Per-server credentials.** Enrollment exchanges the one-time token for a server ID and a random secret, stored at mode `0600`. The control plane keeps only a hash of the secret. Removing a server in Orbit revokes it.
- **Signed commands.** Every command is signed with ed25519 over its ID, the target server, the action, its expiry and a hash of its arguments. The agent pinned the public key at enrollment. A stolen session or a compromised proxy between the two can't forge commands, and a command signed for one server can't be replayed on another.
- **A fixed action list.** The agent rejects any action that isn't on its built-in list, and any command that is expired or reuses an ID. It has no general remote shell.
- **Read-only until asked.** Connecting only reports and scans. Every change starts from a person (or a workflow they saved) in Orbit, checked against their role, and is written to the audit log.
- **A local record.** Every command is also logged on the server itself, in `/var/lib/orbit-agent/actions.log`.

The agent's identity is its secret, not a TLS client certificate (mutual TLS). That keeps it working behind any reverse proxy, including the bundled Caddy, which terminates TLS. The command signatures are what stop a compromised control-plane connection from being used against your servers.

---

## Updating, backups and data

**Update** to a new version:

```bash
git pull
docker compose up -d --build                    # add --profile https if you use Option A
```

Updating keeps everything: users, servers, deployments and history. Database migrations run automatically on start.

**Where data lives.** These Docker volumes have fixed names, so they survive rebuilds:

| Volume | Contents |
|---|---|
| `nolyx-orbit-postgres` | everything Orbit stores |
| `nolyx-orbit-agent` | identity of the agent running next to the control plane |
| `nolyx-orbit-caddy` | TLS certificates (HTTPS profile only) |

Only `docker compose down -v` or `docker volume rm` deletes them. Don't add `-v` unless you want to start over.

**Back up** the database and your `.env`:

```bash
docker compose exec -T postgres pg_dump -U orbit orbit | gzip > orbit-$(date +%F).sql.gz
```

**Restore** onto a fresh install that uses the same `.env`:

```bash
docker compose up -d postgres
gunzip -c orbit-2026-10-31.sql.gz | docker compose exec -T postgres psql -U orbit orbit
docker compose up -d
```

---

## Configuration reference

All settings live in `.env`. After changing them, run `docker compose up -d` (add `--profile https` if you use Option A).

| Variable | Default | Purpose |
|---|---|---|
| `ENCRYPTION_KEY` | — (required) | Encrypts secrets and signs agent commands. Never change it after setup. |
| `APP_URL` | `http://localhost:8080` | The public address of Orbit for browsers and agents. |
| `ORBIT_DOMAIN` | — | Domain for automatic HTTPS (`--profile https`). |
| `ORBIT_BIND` | `0.0.0.0` | Interface port 8080 listens on. Use `127.0.0.1` behind a proxy. |
| `ORBIT_PORT` | `8080` | Host port for the control plane. |
| `POSTGRES_PASSWORD` | `orbit` | Password for the bundled database. Set it before the first start. |
| `ALLOW_SIGNUP` | `false` | Open registration to anyone. The first account and invited people can always sign up. |
| `ALLOW_PRIVATE_WEBHOOKS` | `false` | Let alert webhooks reach private IP addresses (e.g. an on-prem chat server). Off by default so members can't use webhooks to reach internal services. |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM` | — | Email for alerts, invitations and password resets. |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | — | Single sign-on with an OpenID Connect provider. See [Single sign-on](#single-sign-on). |
| `OIDC_NAME` | `SSO` | Label for the "Sign in with …" button, e.g. `Okta`. |

### Single sign-on

Orbit can sign people in with any OpenID Connect provider: Google, Microsoft Entra ID, Okta, Keycloak, Authentik, Auth0.

1. Create a web application (confidential client) at your provider with the redirect URI `APP_URL/api/v1/auth/oidc/callback`, e.g. `https://orbit.example.com/api/v1/auth/oidc/callback`, and the scopes `openid email profile`.
2. Set `OIDC_ISSUER` (the URL that serves `/.well-known/openid-configuration`, e.g. `https://accounts.google.com`, `https://login.microsoftonline.com/<tenant-id>/v2.0`, `https://<org>.okta.com`, `https://keycloak.example.com/realms/<realm>`), `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` and `OIDC_NAME`, then run `docker compose up -d`.

How it works:
- Only verified email addresses are accepted (`email_verified`). Microsoft Entra ID doesn't send that claim: add the optional claims `email` and `xms_edov` to the ID token in the app registration's **Token configuration**, which marks addresses on domains your tenant has verified. Multi-tenant (`/common`) issuers aren't supported.
- The first sign-in links to the Orbit account with the same email. From then on the account only accepts that provider identity, so an email address reassigned at the provider can't take it over. To re-link (e.g. after changing provider), clear `oidc_subject` and `oidc_issuer` for that user in the database.
- Someone without an account gets one only if sign-up is open (`ALLOW_SIGNUP`) or they have a pending invitation, as with passwords.
- People with two-factor authentication still enter their code after signing in with SSO.
- An owner can turn on **Require single sign-on** in **Settings → Organization**. Members of that organization then can't sign in with a password. Owners still can, so nobody is locked out if the provider is down; if SSO is later unconfigured, the requirement stops applying.

**Agent settings** go in `/etc/orbit-agent/env` on each server:

| Variable | Purpose |
|---|---|
| `ORBIT_URL` | The control plane's address (same as `APP_URL`). |
| `ORBIT_TOKEN` | Enrollment token. Only needed the first time. |
| `ORBIT_INSECURE` | Set to `1` to allow plain HTTP. |
| `ORBIT_REGION` | Region label for the server. |
| `NOMAD_ADDR`, `NOMAD_TOKEN` | Location of the Nomad API, if it isn't `http://127.0.0.1:4646`. |

The agent masks likely secrets in container logs before sending them: values of `password`/`secret`/`token`/`api_key`-style keys, `Authorization` headers and bearer tokens, passwords in URLs, private keys, JWTs, AWS access key IDs, and GitHub, Slack, Stripe and Orbit tokens.

---

## Troubleshooting

**The certificate isn't issued.**
- Check `docker compose logs caddy`.
- Confirm DNS resolves to this server and that ports 80 and 443 are reachable from the internet.

**The agent says "ORBIT_URL must be https://".**
- You're using plain HTTP. Add `ORBIT_INSECURE=1` to `/etc/orbit-agent/env` and run `systemctl restart orbit-agent`.

**You changed `APP_URL` (for example, moved from HTTP to HTTPS).** On each server:
1. Update `ORBIT_URL` in `/etc/orbit-agent/env`.
2. Remove `ORBIT_INSECURE` if you moved to HTTPS.
3. Run `systemctl restart orbit-agent`.

The server keeps its identity; no re-enrollment is needed.

**The agent says "rejected agent credentials".**
- The server was removed in Orbit. Delete `/var/lib/orbit-agent/state.json` and install again with a new token.

**Live updates stop behind your own proxy.**
- Turn off response buffering for `/api/v1/projects/*/stream` and allow WebSocket upgrades for `/agent/v1/connect`. See [Option C](#option-c-your-own-reverse-proxy).

**Someone is locked out (forgot their password, or lost their 2FA device and recovery codes).**
- With email configured, they can use **Forgot password?** on the sign-in page. That still asks for their 2FA code.
- Otherwise, run `docker compose exec orbit orbit reset-password them@example.com` and send them the link. It's valid for an hour and also turns off their two-factor authentication.

**"Too many failed sign-in attempts".**
- After 10 failures for one email, or 50 from one IP address, sign-in is paused for 15 minutes. Restarting the control plane clears it.

**Logs to check:**
- Control plane: `docker compose logs -f orbit`
- Agent: `journalctl -u orbit-agent -f`

---

## Development

```bash
docker run -d --name orbit-pg -e POSTGRES_USER=orbit -e POSTGRES_PASSWORD=orbit -p 5432:5432 postgres:17-alpine
DATABASE_URL=postgres://orbit:orbit@localhost:5432/orbit?sslmode=disable ENCRYPTION_KEY=$(openssl rand -hex 32) go run ./cmd/orbit
cd web && npm install && npm run dev        # Vite on :5173, proxies /api to :8080
go test ./... && (cd web && npm run typecheck)
```

Format the UI with `cd web && npm run format`.

### Code layout

**Backend.** Each Go package has one file per area:

| Path | What |
|---|---|
| `cmd/orbit`, `cmd/orbit-agent` | Entry points for the control plane and the agent |
| `internal/protocol` | Messages exchanged by the agent and control plane, split by area: `host`, `commands`, `databases`, `telemetry`, `nomad` |
| `internal/server` | Control plane |
| `internal/agent` | Agent |

Areas in `internal/server`:

- **Core:** `server.go` (setup and migrations), `http.go`, `routes.go`, `crypto.go`
- **Access:** `auth.go`, `sessions.go`, `members.go`, `teams.go`, `apikeys.go`, `audit.go`
- **Agents:** `hub.go`, `agent_session.go`, `commands.go`, `enrollment.go`
- **Infrastructure:** `servers.go`, `containers.go`, `metrics.go`, `overview.go`
- **Services:** `apps.go`, `deployments.go`, `github.go`, `import.go`
- **Databases:** `databases.go`, `database_monitoring.go`, `replicas.go`, `backups.go`, `operations.go`
- **Observability:** `alerts.go`, `alert_rules.go`, `notifications.go`, `logs.go`, `log_forwarding.go`, `monitors.go`
- **Other:** `secrets.go`, `nomad.go`, `cloud.go`, `worker.go`

Areas in `internal/agent`:

- **Connection:** `agent.go` (connection lifecycle), `commands.go`
- **Host:** `collect.go`, `host.go`
- **Docker:** `docker.go`, `docker_ops.go`, `deploy.go`, `import.go`, `proxy.go`, `logs.go`
- **Host:** `journal.go` (service logs), `monitors.go` (checks as a monitor location)
- **PostgreSQL:** `postgres_monitor.go`, `postgres_manage.go`, `postgres_backup.go`
- **Nomad:** `nomad.go`, `nomad_install.go`

**Frontend (`web/src`).** Each page area has its own folder. Dialogs and tabs live in their own files:

| Path | What |
|---|---|
| `main.tsx` | Routes |
| `lib/` | `api.ts` (HTTP and data hooks), `live.ts` (live updates), `types.ts`, `format.ts`, `paths.ts` |
| `components/layout/` | Shell, sidebar, project context, theme |
| `components/ui/` | Shared building blocks: `PageHead`, `Panel`, `status`, `Dialog`, `Confirm`, `controls` … |
| `components/charts/`, `components/containers/` | Charts and the container table and dialog |
| `pages/<area>/` | One folder per area: `overview`, `servers`, `docker`, `nomad`, `services`, `deployments`, `databases` (including `tabs/`), `observability` (including `alerts/`), `security`, `settings`, `operations`, `auth` |

For architecture details, see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).
