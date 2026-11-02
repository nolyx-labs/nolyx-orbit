# Security model

## How the agent connection is secured

- **Outbound only.** The agent dials the control plane over HTTPS (a WebSocket to `ORBIT_URL`, port 443 by default). Managed servers need no inbound ports.
- **Per-server credentials.** Enrollment exchanges the one-time token for a server ID and a random secret, stored at mode `0600`. The control plane keeps only a hash of the secret. Removing a server in Orbit revokes it.
- **Signed commands.** Every command is signed with ed25519 over its ID, the target server, the action, its expiry and a hash of its arguments. The agent pins the public key at enrollment. A stolen session or a compromised proxy can't forge commands, and a command signed for one server can't be replayed on another.
- **A fixed action list.** The agent rejects any action that isn't on its built-in list, and any command that is expired or reuses an ID. It has no general remote shell.
- **Read-only until asked.** Connecting only reports and scans (`apt-get -s`, `dnf --cacheonly`). Every change starts from a person, or a workflow they saved, in Orbit. It's checked against their role and written to the audit log.
- **A local record.** Every command the agent runs is logged on the server in `/var/lib/orbit-agent/actions.log`, one JSON line each: time, command ID, action, result and a SHA-256 of its arguments. The arguments themselves are never logged, because they can hold secrets. The file rotates to `actions.log.1` at 10 MB.

The agent's identity is its secret, not a TLS client certificate (mutual TLS). That keeps it working behind any reverse proxy, including the bundled Caddy, which terminates TLS. The command signatures stop a compromised connection from being used against your servers.

## Plain HTTP

Over HTTP, passwords, session cookies and agent credentials travel unencrypted. Commands are still signed, so they can't be forged, but anyone on the network path can read the traffic. Agents refuse plain HTTP unless `ORBIT_INSECURE=1` is set. Use it only on a private network or VPN.

## Secrets in logs

The agent masks likely secrets in logs before sending them:

- values of `password`, `secret`, `token` and `api_key`-style keys
- `Authorization` headers and bearer tokens
- passwords in URLs
- private keys and JWTs
- AWS access key IDs, and GitHub, Slack, Stripe and Orbit tokens

## Outbound requests

Alert webhooks, uptime checks and log forwarding refuse private, loopback and link-local addresses, so members can't use them to reach internal services. Set `ALLOW_PRIVATE_WEBHOOKS=true` to allow them from the control plane. Checks run from servers as extra monitor locations always refuse private addresses.

## Encryption key

`ENCRYPTION_KEY` encrypts every stored secret and derives the key that signs agent commands. Back it up with your database. If you lose it, stored secrets become unreadable and every agent must re-enroll. Never change it after setup.
