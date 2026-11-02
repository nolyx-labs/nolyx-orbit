import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { Chart } from '../../components/charts/Chart'
import { useCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { RangePicker } from '../../components/ui/controls'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { ago, datetime, duration } from '../../lib/format'
import { useLive } from '../../lib/live'
import { Monitor, MonitorDialog, monitorTone, uptime } from './Monitors'

interface Detail extends Monitor {
  uptime_7d: number | null
  uptime_90d: number | null
  outages: { id: number; started_at: string; ended_at: string | null; error: string }[]
  series: { t: number[]; latency: (number | null)[]; failed: number[] }
  location_results: {
    server_id: string
    name: string
    ok: boolean | null
    latency_ms: number | null
    error: string | null
    ts: string | null
  }[]
}

export function MonitorDetail() {
  const { project } = useProject()
  const { id } = useParams()
  const nav = useNavigate()
  const canEdit = useCan('member')
  const [range, setRange] = useState('24h')
  const [m, error, reload] = useApi<Detail>(`/projects/${project.id}/monitors/${id}?range=${range}`)
  const [editing, setEditing] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState<string>()
  useLive(project.id, ['monitors'], reload, 5000)
  if (error)
    return (
      <div className="page">
        <ErrorNote error={error} />
      </div>
    )
  if (!m) return <Loading />
  const [tone, label] = monitorTone(m)
  const run = (p: Promise<unknown>) => {
    setBusy(true)
    setActionError(undefined)
    p.then(reload, (e) => setActionError(e.message)).finally(() => setBusy(false))
  }
  const certDays = m.cert_expires_at
    ? Math.floor((new Date(m.cert_expires_at).getTime() - Date.now()) / 86400000)
    : null
  return (
    <div className="page">
      <PageHead
        crumbs={<Link to={`/p/${project.id}/monitors`}>Monitors</Link>}
        title={m.name}
        sub={
          <span className="row" style={{ gap: 10 }}>
            <StatusText tone={tone}>{label}</StatusText>
            <span className="faint">·</span>
            <span className="mono muted">{m.target}</span>
            <span className="faint">·</span>
            <span className="muted">every {duration(m.interval_seconds)}</span>
          </span>
        }
      >
        {canEdit && (
          <>
            <button
              className="btn"
              disabled={busy}
              onClick={() => run(api('POST', `/projects/${project.id}/monitors/${m.id}/check`))}
            >
              Check now
            </button>
            <button className="btn" onClick={() => setEditing(true)}>
              Edit
            </button>
            <button className="btn ghost danger" onClick={() => setDeleting(true)}>
              Delete
            </button>
          </>
        )}
      </PageHead>
      <ErrorNote error={actionError} />
      {m.status === 'down' && m.last_error && (
        <div className="callout err" style={{ marginBottom: 16 }}>
          <b style={{ fontWeight: 500 }}>Last check failed:</b> <span className="mono">{m.last_error}</span>
        </div>
      )}
      <div className="stack">
        <Panel title="Uptime">
          <div className="uptime-tiles">
            {(
              [
                ['Last 24 hours', m.uptime_24h],
                ['Last 7 days', m.uptime_7d],
                ['Last 30 days', m.uptime_30d],
                ['Last 90 days', m.uptime_90d],
              ] as const
            ).map(([k, v]) => (
              <div key={k}>
                <div className="k">{k}</div>
                <div className="v mono">{uptime(v)}</div>
              </div>
            ))}
            <div>
              <div className="k">Last response</div>
              <div className="v mono">{m.last_latency_ms != null ? `${m.last_latency_ms} ms` : '—'}</div>
            </div>
            {certDays != null && (
              <div>
                <div className="k">Certificate expires</div>
                <div className={`v mono ${certDays < 14 ? 'text-err' : ''}`}>in {certDays} days</div>
              </div>
            )}
          </div>
        </Panel>
        <Panel title="Response time" actions={<RangePicker value={range} onChange={setRange} />}>
          <div className="panel-body">
            {m.series.t.length ? (
              <Chart
                title={`Average response time from the control plane · checked ${ago(m.last_checked_at)}`}
                t={m.series.t}
                series={[{ label: 'Response', values: m.series.latency, color: 'series-1' }]}
                format={(v) => `${Math.round(v)} ms`}
                integer
              />
            ) : (
              <Empty title="No checks in this range" />
            )}
          </div>
        </Panel>
        {!!m.location_results.length && (
          <Panel title="Locations">
            <table className="t">
              <thead>
                <tr>
                  <th>Checked from</th>
                  <th>Latest result</th>
                  <th className="right">Response</th>
                  <th>When</th>
                </tr>
              </thead>
              <tbody>
                {m.location_results.map((l) => (
                  <tr key={l.server_id}>
                    <td>
                      <Link to={`/p/${project.id}/servers/${l.server_id}/overview`}>{l.name}</Link>
                    </td>
                    <td>
                      {l.ok == null ? (
                        <StatusText tone="">No results yet</StatusText>
                      ) : l.ok ? (
                        <StatusText tone="ok">Up</StatusText>
                      ) : (
                        <StatusText tone="err">{l.error || 'Down'}</StatusText>
                      )}
                    </td>
                    <td className="right mono">{l.latency_ms != null ? `${l.latency_ms} ms` : '—'}</td>
                    <td className="muted">{l.ts ? ago(l.ts) : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Panel>
        )}
        <Panel title="Outages">
          {!m.outages.length ? (
            <Empty title="No outages recorded" />
          ) : (
            <table className="t">
              <thead>
                <tr>
                  <th>Started</th>
                  <th>Lasted</th>
                  <th>Reason</th>
                </tr>
              </thead>
              <tbody>
                {m.outages.map((o) => (
                  <tr key={o.id}>
                    <td className="mono">{datetime(o.started_at)}</td>
                    <td>
                      {o.ended_at ? (
                        duration(Math.round((new Date(o.ended_at).getTime() - new Date(o.started_at).getTime()) / 1000))
                      ) : (
                        <StatusText tone="err">ongoing</StatusText>
                      )}
                    </td>
                    <td className="mono muted">{o.error || '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
      </div>
      {editing && <MonitorDialog monitor={m} onClose={() => setEditing(false)} onSaved={reload} />}
      {deleting && (
        <Confirm
          title={`Delete ${m.name}?`}
          action="Delete monitor"
          danger
          onClose={() => setDeleting(false)}
          onConfirm={() =>
            api('DELETE', `/projects/${project.id}/monitors/${m.id}`).then(() => nav(`/p/${project.id}/monitors`))
          }
        >
          Its check history and outages are deleted too. Status page components that use it show as operational.
        </Confirm>
      )}
    </div>
  )
}
