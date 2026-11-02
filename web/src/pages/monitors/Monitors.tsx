import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useCan, useProject } from '../../components/layout/context'
import { Dialog } from '../../components/ui/Dialog'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { StatusText, Tone } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { ago } from '../../lib/format'
import { useLive } from '../../lib/live'
import { Server } from '../../lib/types'

export interface Monitor {
  id: string
  name: string
  type: 'http' | 'tcp'
  target: string
  interval_seconds: number
  timeout_ms: number
  expected_status: string
  keyword: string
  server_id: string | null
  paused: boolean
  status: 'pending' | 'up' | 'down'
  last_checked_at: string | null
  last_latency_ms: number | null
  last_error: string
  cert_expires_at: string | null
  uptime_24h: number | null
  uptime_30d: number | null
}

export function monitorTone(m: Pick<Monitor, 'status' | 'paused'>): [Tone, string] {
  if (m.paused) return ['', 'Paused']
  return m.status === 'up' ? ['ok', 'Up'] : m.status === 'down' ? ['err', 'Down'] : ['info', 'Checking']
}

export const uptime = (v: number | null | undefined) => (v == null ? '—' : `${v.toFixed(v === 100 ? 0 : 2)}%`)

export function Monitors() {
  const { project } = useProject()
  const nav = useNavigate()
  const canEdit = useCan('member')
  const [monitors, , reload] = useApi<Monitor[]>(`/projects/${project.id}/monitors`)
  const [adding, setAdding] = useState(false)
  useLive(project.id, ['monitors'], reload)
  return (
    <div className="page">
      <PageHead
        title="Monitors"
        sub="Uptime and response time for websites, APIs and ports, checked from this control plane"
      >
        {canEdit && (
          <button className="btn primary" onClick={() => setAdding(true)}>
            New monitor
          </button>
        )}
      </PageHead>
      <Panel>
        {!monitors ? (
          <Loading />
        ) : !monitors.length ? (
          <Empty title="No monitors yet">
            Add a URL or host:port. Two failed checks in a row count as down. Get told about it with an alert rule on
            "Monitor down", and show it on a status page.
          </Empty>
        ) : (
          <table className="t">
            <thead>
              <tr>
                <th>Monitor</th>
                <th>Status</th>
                <th className="right">Response</th>
                <th className="right">24 hours</th>
                <th className="right">30 days</th>
                <th className="right">Checked</th>
              </tr>
            </thead>
            <tbody>
              {monitors.map((m) => {
                const [tone, label] = monitorTone(m)
                return (
                  <tr key={m.id} className="link" onClick={() => nav(`/p/${project.id}/monitors/${m.id}`)}>
                    <td>
                      <div className="name">{m.name}</div>
                      <div className="sub mono">{m.target}</div>
                    </td>
                    <td>
                      <StatusText tone={tone}>{label}</StatusText>
                    </td>
                    <td className="right mono">{m.last_latency_ms != null ? `${m.last_latency_ms} ms` : '—'}</td>
                    <td className="right mono">{uptime(m.uptime_24h)}</td>
                    <td className="right mono">{uptime(m.uptime_30d)}</td>
                    <td className="right muted">{ago(m.last_checked_at)}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </Panel>
      {adding && (
        <MonitorDialog onClose={() => setAdding(false)} onSaved={(id) => nav(`/p/${project.id}/monitors/${id}`)} />
      )}
    </div>
  )
}

export function MonitorDialog({
  monitor,
  onClose,
  onSaved,
}: {
  monitor?: Monitor
  onClose: () => void
  onSaved: (id: string) => void
}) {
  const { project } = useProject()
  const [servers] = useApi<Server[]>(`/projects/${project.id}/servers`)
  const [f, setF] = useState({
    name: monitor?.name ?? '',
    type: monitor?.type ?? 'http',
    target: monitor?.target ?? 'https://',
    interval_seconds: String(monitor?.interval_seconds ?? 60),
    timeout_ms: String(monitor?.timeout_ms ?? 10000),
    expected_status: monitor?.expected_status ?? '2xx',
    keyword: monitor?.keyword ?? '',
    server_id: monitor?.server_id ?? '',
    paused: monitor?.paused ?? false,
  })
  const [error, setError] = useState<string>()
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setF({ ...f, [k]: e.target.value })
  const save = () =>
    api<{ id: string }>(
      monitor ? 'PUT' : 'POST',
      `/projects/${project.id}/monitors${monitor ? `/${monitor.id}` : ''}`,
      {
        ...f,
        interval_seconds: +f.interval_seconds,
        timeout_ms: +f.timeout_ms,
      },
    ).then(
      (r) => {
        onSaved(r.id)
        onClose()
      },
      (e) => setError(e.message),
    )
  return (
    <Dialog
      title={monitor ? `Edit ${monitor.name}` : 'New monitor'}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" disabled={!f.name || !f.target} onClick={save}>
            {monitor ? 'Save' : 'Create'}
          </button>
        </>
      }
    >
      <div className="grid-2">
        <label className="field">
          <span>Name</span>
          <input className="input" value={f.name} onChange={set('name')} placeholder="Marketing site" autoFocus />
        </label>
        <label className="field">
          <span>Type</span>
          <select
            className="select"
            value={f.type}
            onChange={(e) =>
              setF({
                ...f,
                type: e.target.value as 'http' | 'tcp',
                target: e.target.value === 'http' ? 'https://' : '',
              })
            }
          >
            <option value="http">HTTP(S): a web address responds</option>
            <option value="tcp">TCP: a port accepts connections</option>
          </select>
        </label>
      </div>
      <label className="field">
        <span>{f.type === 'http' ? 'URL' : 'Host and port'}</span>
        <input
          className="input mono"
          value={f.target}
          onChange={set('target')}
          placeholder={f.type === 'http' ? 'https://example.com/health' : 'db.example.com:5432'}
        />
      </label>
      {f.type === 'http' && (
        <div className="grid-2">
          <label className="field">
            <span>Expected status</span>
            <input className="input mono" value={f.expected_status} onChange={set('expected_status')} />
            <span className="hint">For example 2xx, 200 or 200-299,301.</span>
          </label>
          <label className="field">
            <span>Response must contain (optional)</span>
            <input className="input" value={f.keyword} onChange={set('keyword')} placeholder="OK" />
          </label>
        </div>
      )}
      <div className="grid-2">
        <label className="field">
          <span>Check every</span>
          <select className="select" value={f.interval_seconds} onChange={set('interval_seconds')}>
            {[30, 60, 120, 300, 600, 1800, 3600].map((s) => (
              <option key={s} value={s}>
                {s < 60 ? `${s} seconds` : s === 60 ? '1 minute' : s < 3600 ? `${s / 60} minutes` : '1 hour'}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>Timeout</span>
          <select className="select" value={f.timeout_ms} onChange={set('timeout_ms')}>
            {[3000, 5000, 10000, 20000, 30000].map((ms) => (
              <option key={ms} value={ms}>
                {ms / 1000} seconds
              </option>
            ))}
          </select>
        </label>
      </div>
      <label className="field">
        <span>Runs on server (optional)</span>
        <select className="select" value={f.server_id} onChange={set('server_id')}>
          <option value="">None</option>
          {servers?.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
        <span className="hint">
          Maintenance on that server's status page component also pauses this monitor's alerts.
        </span>
      </label>
      {monitor && (
        <div className="checkbox-row">
          <label>
            <input type="checkbox" checked={f.paused} onChange={(e) => setF({ ...f, paused: e.target.checked })} />
            Paused (no checks, and paused time doesn't count against uptime)
          </label>
        </div>
      )}
      <ErrorNote error={error} />
    </Dialog>
  )
}
