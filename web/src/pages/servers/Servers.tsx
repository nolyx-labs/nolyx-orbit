import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useProject } from '../../components/layout/context'
import { Meter } from '../../components/ui/Meter'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { StatusText, serverTone } from '../../components/ui/status'
import { useApi } from '../../lib/api'
import { ago, bitrate, bytes, ratio } from '../../lib/format'
import { useLive } from '../../lib/live'
import { Server } from '../../lib/types'

export function Servers() {
  const { project } = useProject()
  const nav = useNavigate()
  const [all, , reload] = useApi<Server[]>(`/projects/${project.id}/servers`)
  const [region, setRegion] = useState('')
  useLive(project.id, ['servers', 'metrics', 'containers'], reload, 5000)
  const regions = [...new Set(all?.map((s) => s.region).filter(Boolean))].sort()
  const servers = all?.filter((s) => !region || s.region === region)

  return (
    <div className="page">
      <PageHead title="Servers" sub="Machines running the Orbit agent">
        {regions.length > 0 && (
          <select
            className="select"
            style={{ width: 160 }}
            value={region}
            onChange={(e) => setRegion(e.target.value)}
            aria-label="Region"
          >
            <option value="">All regions</option>
            {regions.map((r) => (
              <option key={r}>{r}</option>
            ))}
          </select>
        )}
        <Link className="btn primary" to={`/p/${project.id}/settings/agents`}>
          Add server
        </Link>
      </PageHead>
      <Panel>
        {!servers ? (
          <Loading />
        ) : servers.length === 0 ? (
          <Empty title="No servers yet">
            <div style={{ marginTop: 8 }}>
              <Link className="btn sm" to={`/p/${project.id}/settings/agents`}>
                Install the agent
              </Link>
            </div>
          </Empty>
        ) : (
          <div className="table-wrap">
            <table className="t">
              <thead>
                <tr>
                  <th>Server</th>
                  <th>Status</th>
                  <th>Region</th>
                  <th>System</th>
                  <th>CPU</th>
                  <th>Memory</th>
                  <th>Disk</th>
                  <th className="right">Network</th>
                  <th className="right">Containers</th>
                  <th className="right">Heartbeat</th>
                </tr>
              </thead>
              <tbody>
                {servers.map((s) => {
                  const on = s.status === 'online'
                  return (
                    <tr key={s.id} className="link" onClick={() => nav(`/p/${project.id}/servers/${s.id}/overview`)}>
                      <td>
                        <div className="name">{s.name}</div>
                        <div className="sub mono">{s.ip || s.hostname}</div>
                      </td>
                      <td>
                        <StatusText tone={serverTone(s)}>{on ? 'Online' : 'Offline'}</StatusText>
                      </td>
                      <td className="muted">
                        {s.region || '—'}
                        {s.provider && <span className="faint"> · {s.provider}</span>}
                      </td>
                      <td>
                        <div>{s.os || '—'}</div>
                        <div className="sub mono">
                          {s.arch} · {s.cpu_count} CPU · {bytes(s.mem_total, 0)}
                        </div>
                      </td>
                      <td>
                        <Meter value={on ? s.cpu : null} />
                      </td>
                      <td>
                        <Meter value={on ? ratio(s.mem_used, s.mem_total) : null} />
                      </td>
                      <td>
                        <Meter value={ratio(s.disk_used, s.disk_total)} />
                      </td>
                      <td className="right mono muted">
                        {on && s.net_rx != null ? bitrate((s.net_rx ?? 0) + (s.net_tx ?? 0)) : '—'}
                      </td>
                      <td className="right mono">{s.containers_running}</td>
                      <td className="right muted nowrap">{ago(s.last_seen_at)}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
    </div>
  )
}
