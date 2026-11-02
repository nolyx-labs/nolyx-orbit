import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { Panel } from '../../components/ui/Panel'
import { api, useApi } from '../../lib/api'
import { Server } from '../../lib/types'
import { ENVIRONMENTS, ServerGroup } from './ServerGroups'

function ServerEdit({ s }: { s: Server }) {
  const { project } = useProject()
  const [groups] = useApi<ServerGroup[]>(`/projects/${project.id}/server-groups`)
  const initial = { name: s.name, region: s.region, environment: s.environment }
  const initialGroups = s.groups.map((g) => g.id).sort()
  const [f, setF] = useState(initial)
  const [picked, setPicked] = useState(initialGroups)
  const [msg, setMsg] = useState<string>()
  const groupsChanged = picked.join() !== initialGroups.join()
  const changed = JSON.stringify(f) !== JSON.stringify(initial)
  const save = async () => {
    if (changed) await api('PATCH', `/projects/${project.id}/servers/${s.id}`, f)
    if (groupsChanged) await api('PUT', `/projects/${project.id}/servers/${s.id}/groups`, { group_ids: picked })
  }
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
        <label className="field" style={{ width: 180 }}>
          <span>Environment</span>
          <select
            className="select"
            value={f.environment}
            onChange={(e) => setF({ ...f, environment: e.target.value as Server['environment'] })}
          >
            {ENVIRONMENTS.map((e) => (
              <option key={e}>{e}</option>
            ))}
          </select>
        </label>
        <button
          className="btn"
          style={{ alignSelf: 'flex-end' }}
          disabled={!changed && !groupsChanged}
          onClick={() =>
            save().then(
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
      <div className="panel-body checkbox-row" style={{ flexWrap: 'wrap', paddingTop: 0 }}>
        <span className="muted">Groups:</span>
        {!groups?.length && <span className="faint">none yet — create them on the Servers page</span>}
        {groups?.map((g) => (
          <label key={g.id}>
            <input
              type="checkbox"
              checked={picked.includes(g.id)}
              onChange={(e) =>
                setPicked(e.target.checked ? [...picked, g.id].sort() : picked.filter((x) => x !== g.id))
              }
            />
            {g.name}
          </label>
        ))}
      </div>
    </Panel>
  )
}

export function ServerConfig({ s }: { s: Server }) {
  const { project } = useProject()
  const canAdmin = useCan('admin')
  const nav = useNavigate()
  const [confirm, setConfirm] = useState<'remove' | 'clear'>()
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
              <div style={{ fontWeight: 500 }}>Clear collected data</div>
              <div className="muted">
                Deletes this server's metrics history, container logs and events. The server stays connected.
              </div>
            </div>
            <button className="btn danger" onClick={() => setConfirm('clear')}>
              Clear data
            </button>
          </div>
          <div className="panel-body row">
            <div style={{ flex: 1 }}>
              <div style={{ fontWeight: 500 }}>Remove server</div>
              <div className="muted">
                Revokes the agent's credentials and deletes metrics history. Running containers on the server are not
                touched.
              </div>
            </div>
            <button className="btn danger" onClick={() => setConfirm('remove')}>
              Remove server
            </button>
          </div>
        </Panel>
      )}
      {confirm === 'clear' && (
        <Confirm
          title={`Clear data for ${s.name}?`}
          action="Clear data"
          danger
          typeToConfirm={s.name}
          onClose={() => setConfirm(undefined)}
          onConfirm={() => api('POST', `/projects/${project.id}/servers/${s.id}/clear-data`, { confirm: s.name })}
        >
          Metrics, container logs and the event timeline for this server are deleted for good. The audit log keeps a
          record that you did this.
        </Confirm>
      )}
      {confirm === 'remove' && (
        <Confirm
          title={`Remove ${s.name}?`}
          action="Remove server"
          danger
          typeToConfirm={s.name}
          onClose={() => setConfirm(undefined)}
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
          The agent will be disconnected and can't reconnect. To uninstall it, run this on the server:
          <div className="mono" style={{ margin: '6px 0', userSelect: 'all' }}>
            sudo systemctl disable --now orbit-agent && sudo rm -rf /etc/systemd/system/orbit-agent.service
            /usr/local/bin/orbit-agent /etc/orbit-agent /var/lib/orbit-agent
          </div>
          If it runs as a container: <span className="mono">docker rm -f orbit-agent && docker volume rm orbit-agent</span>
          . To add the server back later instead, delete <span className="mono">/var/lib/orbit-agent/state.json</span>{' '}
          and re-enroll with a new token.
        </Confirm>
      )}
    </div>
  )
}
