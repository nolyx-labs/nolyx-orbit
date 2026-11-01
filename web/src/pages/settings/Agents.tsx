import { useState } from 'react'
import { useCan, useProject } from '../../components/layout/context'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Copy } from '../../components/ui/controls'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { api, useApi } from '../../lib/api'
import { ago, datetime } from '../../lib/format'
import { CloudServers } from './CloudServers'

interface Token {
  id: string
  expires_at: string
  last_used_at: string | null
  created_at: string
  created_by: string
}

export function Agents() {
  const { project } = useProject()
  const canAdmin = useCan('admin')
  const [tokens, error, reload] = useApi<Token[]>(canAdmin ? `/projects/${project.id}/enrollment-tokens` : null)
  const [fresh, setFresh] = useState<{ token: string; app_url: string } | null>(null)
  const [err, setErr] = useState<string>()

  if (!canAdmin)
    return (
      <div className="page">
        <PageHead title="Agents" />
        <Panel pad>
          <span className="muted">Adding servers requires the admin role.</span>
        </Panel>
      </div>
    )

  const create = () =>
    api('POST', `/projects/${project.id}/enrollment-tokens`).then(
      (t) => {
        setFresh(t)
        reload()
      },
      (e) => setErr(e.message),
    )
  const insecure = fresh?.app_url.startsWith('http://')
  const install =
    fresh &&
    `curl -fsSL ${fresh.app_url}/install.sh | sudo ORBIT_TOKEN=${fresh.token}${insecure ? ' ORBIT_INSECURE=1' : ''} ORBIT_REGION=eu-central sh`
  const dockerRun =
    fresh &&
    `docker run -d --name orbit-agent --restart unless-stopped --network host \\
  -v /var/run/docker.sock:/var/run/docker.sock -v /:/host:ro -v orbit-agent:/var/lib/orbit-agent \\
  -e ORBIT_URL=${fresh.app_url} -e ORBIT_TOKEN=${fresh.token} -e ORBIT_HOST_ROOT=/host -e ORBIT_REGION=eu-central${insecure ? ' -e ORBIT_INSECURE=1' : ''} \\
  nolyx-orbit/agent:latest`

  return (
    <div className="page">
      <PageHead title="Agents" sub="Connect servers to this project" />
      <div className="stack">
        <Panel title="Add an existing server">
          <div className="panel-body stack" style={{ gap: 12 }}>
            <div className="muted" style={{ maxWidth: 720 }}>
              The agent opens an outbound TLS connection to this control plane. Your server needs no inbound ports.
              Commands it runs are signed by the control plane and pinned at enrollment. Enrollment tokens are valid for
              24 hours and can enroll multiple servers. <span className="mono">ORBIT_REGION</span> is optional.
            </div>
            {!fresh ? (
              <div>
                <button className="btn primary" onClick={create}>
                  Generate enrollment token
                </button>
              </div>
            ) : (
              <>
                <div className="callout warn">Copy this now. The token is shown only once.</div>
                {insecure && (
                  <div className="callout warn">
                    This control plane uses plain HTTP, so agent traffic isn't encrypted. Use it only on a trusted
                    network, or set up HTTPS (see the README).
                  </div>
                )}
                <div className="field">
                  <span>Install with systemd (Linux, amd64/arm64)</span>
                  <div className="copy-row">
                    <pre className="code">{install}</pre>
                    <Copy text={install!} />
                  </div>
                </div>
                <div className="field">
                  <span>Or run as a container</span>
                  <div className="copy-row">
                    <pre className="code">{dockerRun}</pre>
                    <Copy text={dockerRun!} />
                  </div>
                </div>
              </>
            )}
            <ErrorNote error={err} />
          </div>
        </Panel>
        <CloudServers />
        <Panel title="Active enrollment tokens">
          <ErrorNote error={error} />
          {!tokens ? (
            <Loading />
          ) : !tokens.length ? (
            <Empty title="No active tokens" />
          ) : (
            <table className="t">
              <thead>
                <tr>
                  <th>Token</th>
                  <th>Created by</th>
                  <th>Created</th>
                  <th>Expires</th>
                  <th>Last used</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {tokens.map((t) => (
                  <tr key={t.id}>
                    <td className="mono">
                      orb_enr_••••<span className="faint"> {t.id.slice(0, 8)}</span>
                    </td>
                    <td className="muted">{t.created_by ?? 'cloud provisioning'}</td>
                    <td className="muted">{ago(t.created_at)}</td>
                    <td className="muted">{datetime(t.expires_at)}</td>
                    <td className="muted">{t.last_used_at ? ago(t.last_used_at) : 'never'}</td>
                    <td className="right">
                      <button
                        className="btn ghost sm danger"
                        onClick={() => api('DELETE', `/projects/${project.id}/enrollment-tokens/${t.id}`).then(reload)}
                      >
                        Revoke
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
      </div>
    </div>
  )
}
