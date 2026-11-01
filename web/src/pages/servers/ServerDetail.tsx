import { Link, NavLink, Navigate, useParams } from 'react-router-dom'
import { useProject } from '../../components/layout/context'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { StatusText, serverTone } from '../../components/ui/status'
import { useApi } from '../../lib/api'
import { ago } from '../../lib/format'
import { useLive } from '../../lib/live'
import { Server } from '../../lib/types'
import { ServerConfig } from './ServerConfig'
import { ServerContainers } from './ServerContainers'
import { ServerEvents } from './ServerEvents'
import { ServerMetrics } from './ServerMetrics'
import { ServerOverview } from './ServerOverview'
import { ServerServices } from './ServerServices'

const SERVER_TABS = ['overview', 'containers', 'services', 'metrics', 'logs', 'events', 'terminal', 'configuration']

export function ServerDetail() {
  const { project } = useProject()
  const { sid, tab = 'overview' } = useParams()
  const [s, error, reload] = useApi<Server>(`/projects/${project.id}/servers/${sid}`)
  useLive(project.id, ['servers', 'metrics'], reload, 5000)

  if (error)
    return (
      <div className="page">
        <PageHead title="Server" />
        <Empty title={error} />
      </div>
    )
  if (!s)
    return (
      <div className="page">
        <Loading />
      </div>
    )
  const base = `/p/${project.id}/servers/${sid}`
  return (
    <div className="page">
      <PageHead
        crumbs={<Link to={`/p/${project.id}/servers`}>Servers</Link>}
        title={s.name}
        sub={
          <span className="row" style={{ gap: 10 }}>
            <StatusText tone={serverTone(s)}>{s.status === 'online' ? 'Healthy' : 'Offline'}</StatusText>
            <span className="faint">·</span>
            <span className="mono muted">{s.ip}</span>
            <span className="faint">·</span>
            <span className="muted">heartbeat {ago(s.last_seen_at)}</span>
          </span>
        }
      />
      <div className="tabs">
        {SERVER_TABS.map((t) => (
          <NavLink key={t} to={`${base}/${t}`} className={t === tab ? 'active' : ''}>
            {t[0].toUpperCase() + t.slice(1)}
          </NavLink>
        ))}
      </div>
      {tab === 'overview' && <ServerOverview s={s} />}
      {tab === 'containers' && <ServerContainers sid={s.id} />}
      {tab === 'services' && <ServerServices sid={s.id} />}
      {tab === 'metrics' && <ServerMetrics sid={s.id} />}
      {tab === 'logs' && <Navigate to={`/p/${project.id}/logs?server=${s.id}`} replace />}
      {tab === 'events' && <ServerEvents sid={s.id} />}
      {tab === 'terminal' && (
        <Panel pad>
          <div style={{ fontWeight: 500, marginBottom: 4 }}>No remote shell, by design</div>
          <div className="muted" style={{ maxWidth: 680 }}>
            The agent only accepts an allowlisted set of audited commands (container start/stop/restart/remove, logs,
            deploy). An interactive terminal would turn the control plane into a root shell on every server. If it's
            added, it will be opt-in per server, require admin role and re-authentication, and record every session.
          </div>
        </Panel>
      )}
      {tab === 'configuration' && <ServerConfig s={s} />}
    </div>
  )
}
