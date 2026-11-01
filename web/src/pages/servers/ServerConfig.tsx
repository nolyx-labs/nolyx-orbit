import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { Panel } from '../../components/ui/Panel'
import { api } from '../../lib/api'
import { Server } from '../../lib/types'

function ServerEdit({ s }: { s: Server }) {
  const { project } = useProject()
  const [f, setF] = useState({ name: s.name, region: s.region })
  const [msg, setMsg] = useState<string>()
  return (
    <Panel title="General">
      <div className="panel-body row" style={{ flexWrap: 'wrap' }}>
        <label className="field" style={{ width: 240 }}>
          <span>Name</span>
          <input className="input" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
        </label>
        <label className="field" style={{ width: 200 }}>
          <span>Region</span>
          <input
            className="input mono"
            placeholder="eu-central"
            value={f.region}
            onChange={(e) => setF({ ...f, region: e.target.value })}
          />
        </label>
        <button
          className="btn"
          style={{ alignSelf: 'flex-end' }}
          disabled={f.name === s.name && f.region === s.region}
          onClick={() =>
            api('PATCH', `/projects/${project.id}/servers/${s.id}`, f).then(
              () => setMsg('Saved'),
              (e) => setMsg(e.message),
            )
          }
        >
          Save
        </button>
        {msg && (
          <span className="faint" style={{ alignSelf: 'flex-end' }}>
            {msg}
          </span>
        )}
      </div>
    </Panel>
  )
}

export function ServerConfig({ s }: { s: Server }) {
  const { project } = useProject()
  const canAdmin = useCan('admin')
  const nav = useNavigate()
  const [confirm, setConfirm] = useState(false)
  return (
    <div className="stack">
      {canAdmin && <ServerEdit s={s} />}
      <Panel title="Agent">
        <div className="panel-body kv">
          <div className="k">Server ID</div>
          <div className="mono">{s.id}</div>
          <div className="k">Agent version</div>
          <div className="mono">{s.agent_version}</div>
          <div className="k">Enrolled</div>
          <div>{new Date(s.created_at).toLocaleString()}</div>
          <div className="k">Connection</div>
          <div className="muted">Outbound WebSocket from agent; no inbound ports required.</div>
        </div>
      </Panel>
      {canAdmin && (
        <Panel title="Danger zone">
          <div className="panel-body row">
            <div style={{ flex: 1 }}>
              <div style={{ fontWeight: 500 }}>Remove server</div>
              <div className="muted">
                Revokes the agent's credentials and deletes metrics history. Running containers on the server are not
                touched.
              </div>
            </div>
            <button className="btn danger" onClick={() => setConfirm(true)}>
              Remove server
            </button>
          </div>
        </Panel>
      )}
      {confirm && (
        <Confirm
          title={`Remove ${s.name}?`}
          action="Remove server"
          danger
          typeToConfirm={s.name}
          onClose={() => setConfirm(false)}
          onConfirm={() =>
            api('DELETE', `/projects/${project.id}/servers/${s.id}`).then(() => nav(`/p/${project.id}/servers`))
          }
        >
          {s.provider && (
            <div className="callout warn" style={{ marginBottom: 8 }}>
              This server was created in {s.provider}. Removing it here does not delete the cloud server; destroy it
              from Settings → Agents so Hetzner stops charging for it.
            </div>
          )}
          The agent will be disconnected and can't reconnect. To add it back, delete{' '}
          <span className="mono">/var/lib/orbit-agent/state.json</span> and re-enroll with a new token.
        </Confirm>
      )}
    </div>
  )
}
