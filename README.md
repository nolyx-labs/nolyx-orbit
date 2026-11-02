# Nolyx Orbit

**Self-hosted infrastructure management, without the complexity of Kubernetes.**

Orbit is a control plane you run on your own server. You install a small agent on each machine you want to manage. The agent connects out to Orbit, so those machines need no open inbound ports.

## Features

- **Deploy:** Docker services with zero-downtime deploys, Git push-to-deploy, releases and rollbacks. Import containers and Compose services that are already running.
- **Orchestrate:** single servers, or a Nomad cluster.
- **Databases:** monitor PostgreSQL, add and promote read replicas, and schedule backups, including to S3.
- **Observe:** metrics, searchable logs from containers and system services, uptime monitors checked from several locations, and alerts by email, Slack, Discord or webhook. Logs can also be copied into your own PostgreSQL.
- **Status pages:** public pages with 90-day uptime, incidents, scheduled maintenance, subscribers and custom domains.
- **Secure:** secrets, API keys, teams, custom roles, single sign-on, two-factor authentication and a full audit log.
- **Provision:** create Hetzner Cloud servers with the agent already installed.

## Contents

- [Quick start](#quick-start)
- [Connect your servers](#connect-your-servers)
- [Other ways to expose Orbit](#other-ways-to-expose-orbit)
- [Update, back up and restore](#update-back-up-and-restore)
- [Configuration](#configuration)
- [Troubleshooting](#troubleshooting)
- [Further reading](#further-reading)

## Quick start

### Requirements

- **Control plane:** a Linux server with Docker Engine and the Docker Compose plugin, 1 vCPU and 1 GB of RAM (2 GB recommended).
- **Managed servers:** Linux (amd64 or arm64) with systemd, or Docker if you run the agent as a container. They only need to reach Orbit's URL.
- **A domain name** pointing at the control plane, for automatic HTTPS. To try Orbit without one, see [Plain HTTP](#plain-http).

### 1. Download and configure

```bash
git clone <this repository> nolyx-orbit && cd nolyx-orbit
cp .env.example .env
sed -i.bak "s/^ENCRYPTION_KEY=.*/ENCRYPTION_KEY=$(openssl rand -hex 32)/" .env && rm .env.bak
```

Then edit `.env`:

```env
APP_URL=https://orbit.example.com
ORBIT_DOMAIN=orbit.example.com
ORBIT_BIND=127.0.0.1
POSTGRES_PASSWORD=<a strong password>
```

> **Back up `.env` now.** `ENCRYPTION_KEY` protects every stored secret and signs every command sent to your servers. If you lose it, stored secrets can't be recovered and every server has to be connected again.

### 2. Point your domain at the server

- Create an `A` record (and an `AAAA` record for IPv6) from `orbit.example.com` to the server's public IP.
- Open ports `80` and `443` (TCP, plus UDP 443 for HTTP/3). Port 80 must stay open so the certificate can renew.

### 3. Start Orbit

```bash
docker compose --profile https up -d --build
```

The bundled [Caddy](https://caddyserver.com) proxy gets a Let's Encrypt certificate automatically. The first visit can take a few seconds while it's issued.

### 4. Create your account

Open `https://orbit.example.com/signup`. The first account becomes the owner, and sign-up then closes. Invite everyone else from **Security → Access → Invite**.

Turn on two-factor authentication in **Settings → Account**.

## Connect your servers

1. In Orbit, open **Settings → Agents → Generate enrollment token**. A token is valid for 24 hours and can connect several servers.
2. On each server, run the install command shown on that page:

   ```bash
   curl -fsSL https://orbit.example.com/install.sh | sudo ORBIT_TOKEN=orb_enr_… sh
   ```

The server appears under **Servers** within seconds, and its containers, databases and logs follow shortly after.

**Good to know:**

- **Connecting changes nothing.** The agent only reports what the server runs and scans it. Nothing changes until someone acts in Orbit.
- **Labels are optional.** Add `ORBIT_REGION=eu-central` to the command to label the server with a region.
- **To see the agent's logs,** run `journalctl -u orbit-agent -f`.
- **To remove the agent,** run `sudo orbit-agent disconnect`. Your containers and services keep running.
- **To manage the control-plane machine itself,** run:

  ```bash
  ORBIT_TOKEN=orb_enr_… docker compose --profile agent up -d --build agent
  ```

- **To create a new server in Hetzner Cloud,** add a Hetzner API token in **Settings → Organization**, then use **Settings → Agents → Create server**.

For how the connection is protected, see [docs/SECURITY.md](docs/SECURITY.md).

## Other ways to expose Orbit

`APP_URL` is the address that both browsers and agents use. Set it before you connect servers. If you change it later, you have to update every agent (see [Troubleshooting](#troubleshooting)).

### Plain HTTP

This is for trials, private networks and VPNs such as Tailscale or WireGuard. **Don't use it over the public internet:** passwords and credentials would travel unencrypted.

```env
APP_URL=http://192.168.1.20:8080
```

```bash
docker compose up -d --build
```

Open port `8080` only to the machines that need it. Install commands for plain HTTP include `ORBIT_INSECURE=1`, which agents need before they'll connect without TLS.

### Your own reverse proxy

If you already run nginx, Traefik or a load balancer, set `APP_URL=https://your-domain` and `ORBIT_BIND=127.0.0.1`, start Orbit without the `https` profile, and proxy to port 8080. The proxy must:

- allow WebSocket upgrades on `/agent/v1/connect`
- not buffer `/api/v1/projects/*/stream` (live updates)
- pass the `Host` header through, so status page custom domains keep working

<details>
<summary>nginx example</summary>

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
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_buffering off;
        proxy_read_timeout 1h;
    }
}
```

</details>

### Status page domains

To serve a status page at its own domain, such as `status.example.com`:

1. Point a `CNAME` record at `ORBIT_DOMAIN`.
2. Enter the domain in the status page's **Settings → Custom domain**.

Caddy issues the certificate on the first visit.

## Update, back up and restore

**Update:**

```bash
git pull
docker compose --profile https up -d --build    # drop --profile https if you don't use it
```

Your data is kept and database migrations run automatically. Always include the same `--profile` flags you started with.

**Back up** the database, and keep a copy of `.env` with it:

```bash
docker compose exec -T postgres pg_dump -U orbit orbit | gzip > orbit-$(date +%F).sql.gz
```

**Restore** onto a fresh install that uses the same `.env`:

```bash
docker compose up -d postgres
gunzip -c orbit-2026-10-31.sql.gz | docker compose exec -T postgres psql -U orbit orbit
docker compose up -d
```

Data lives in the Docker volumes `nolyx-orbit-postgres` (everything Orbit stores), `nolyx-orbit-agent` (the local agent's identity) and `nolyx-orbit-caddy` (certificates). They survive rebuilds. Only `docker compose down -v` deletes them.

## Configuration

Settings live in `.env`. After a change, run `docker compose up -d` with your usual `--profile` flags.

| Variable | Default | Purpose |
|---|---|---|
| `ENCRYPTION_KEY` | required | Encrypts secrets and signs agent commands. Never change it after setup. |
| `APP_URL` | `http://localhost:8080` | Orbit's address for browsers and agents |
| `ORBIT_DOMAIN` | | Domain for automatic HTTPS (`--profile https`) |
| `ORBIT_BIND` | `0.0.0.0` | Interface port 8080 listens on. Use `127.0.0.1` behind a proxy. |
| `ORBIT_PORT` | `8080` | Host port for the control plane |
| `POSTGRES_PASSWORD` | `orbit` | Password for the bundled database. Set it before the first start. |
| `ALLOW_SIGNUP` | `false` | Let anyone register. The first account and invited people can always sign up. |
| `ALLOW_PRIVATE_WEBHOOKS` | `false` | Let webhooks, uptime checks and log copying reach private IP addresses |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM` | | Email for alerts, invitations and password resets |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_NAME` | | Single sign-on. See [docs/SSO.md](docs/SSO.md). |

**Agent settings** live in `/etc/orbit-agent/env` on each server. Restart the agent after changing them with `systemctl restart orbit-agent`.

| Variable | Purpose |
|---|---|
| `ORBIT_URL` | Orbit's address (the same as `APP_URL`) |
| `ORBIT_TOKEN` | Enrollment token, needed only the first time |
| `ORBIT_INSECURE` | `1` allows plain HTTP |
| `ORBIT_REGION` | Region label for the server |
| `NOMAD_ADDR`, `NOMAD_TOKEN` | Nomad API location, if it isn't `http://127.0.0.1:4646` |

## Troubleshooting

| Problem | Fix |
|---|---|
| The HTTPS certificate isn't issued | Run `docker compose logs caddy`. Check that DNS points to this server and that ports 80 and 443 are reachable from the internet. |
| The agent says `ORBIT_URL must be https://` | You're on plain HTTP. Add `ORBIT_INSECURE=1` to `/etc/orbit-agent/env` and restart the agent. |
| The agent says `rejected agent credentials` | The server was removed in Orbit. Delete `/var/lib/orbit-agent/state.json` and install again with a new token. |
| You changed `APP_URL` | On each server, update `ORBIT_URL` in `/etc/orbit-agent/env`, remove `ORBIT_INSECURE` if you moved to HTTPS, and restart the agent. No re-enrollment is needed. |
| Live updates stop behind your own proxy | Turn off buffering for `/api/v1/projects/*/stream` and allow WebSockets on `/agent/v1/connect`. |
| Someone is locked out | With email set up, they can use **Forgot password?**, which still asks for their 2FA code. Otherwise run `docker compose exec orbit orbit reset-password them@example.com` and send them the link. It's valid for an hour and turns off their 2FA. |
| "Too many failed sign-in attempts" | Sign-in pauses for 15 minutes after 10 failures for one email, or 50 from one IP. Restarting the control plane clears it. |

To see logs, run `docker compose logs -f orbit` for the control plane, or `journalctl -u orbit-agent -f` for an agent.

## Further reading

- [Security model](docs/SECURITY.md): how agents and commands are protected, and what's masked in logs
- [Single sign-on](docs/SSO.md): connecting Google, Microsoft Entra ID, Okta, Keycloak and others
- [Architecture](docs/ARCHITECTURE.md): how the control plane and agents work, and known limits
- [Development](docs/DEVELOPMENT.md): running Orbit locally, tests and code layout
