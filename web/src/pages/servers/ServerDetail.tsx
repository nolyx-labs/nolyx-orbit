import { Link, NavLink, Navigate, useParams } from 'react-router'
import { useProject } from '../../components/layout/context'
import { PageHead } from '../../components/ui/PageHead'
import { Empty, Loading } from '../../components/ui/states'
import { StatusText, serverTone } from '../../components/ui/status'
import { useApi } from '../../lib/api'
import { ago } from '../../lib/format'
import { useLive } from '../../lib/live'
import { Server } from '../../lib/types'
import { ServerBackups } from './ServerBackups'
import { ServerConfig } from './ServerConfig'
import { ServerContainers } from './ServerContainers'
import { ServerDisk } from './ServerDisk'
import { ServerEvents } from './ServerEvents'
import { MaintenanceButton, MaintenanceWindows } from './ServerMaintenance'
import { ServerMetrics } from './ServerMetrics'
import { ServerOverview } from './ServerOverview'
import { ServerPorts } from './ServerPorts'
import { ServerSecurity } from './ServerSecurity'
import { ServerServices } from './ServerServices'
import { ServerSpecs } from './ServerSpecs'
import { ServerSystem } from './ServerSystem'
import { ServerUpdates } from './ServerUpdates'

const SERVER_TABS = [
  'overview',
  'containers',
  'services',
  'system',
  'metrics',
  'logs',
  'events',
  'security',
  'disk',
  'backups',
  'configuration',
  'updates',
]

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
      >
        <MaintenanceButton s={s} />
      </PageHead>
      <div className="tabs">
        {SERVER_TABS.map((t) => (
          <NavLink key={t} to={`${base}/${t}`} className={t === tab ? 'active' : ''}>
            {t[0].toUpperCase() + t.slice(1)}
          </NavLink>
        ))}
      </div>
      {tab === 'overview' && <MaintenanceWindows s={s} />}
      {tab === 'overview' && <ServerOverview s={s} />}
      {tab === 'containers' && <ServerContainers sid={s.id} />}
      {tab === 'services' && <ServerServices sid={s.id} />}
      {tab === 'metrics' && <ServerMetrics sid={s.id} />}
      {tab === 'logs' && <Navigate to={`/p/${project.id}/logs?server=${s.id}`} replace />}
      {tab === 'events' && <ServerEvents sid={s.id} />}
      {tab === 'system' && (
        <div className="stack">
          <ServerSystem sid={s.id} />
          <ServerPorts sid={s.id} />
        </div>
      )}
      {tab === 'security' && <ServerSecurity sid={s.id} />}
      {tab === 'disk' && (
        <div className="stack">
          <ServerSpecs sid={s.id} />
          <ServerDisk sid={s.id} />
        </div>
      )}
      {tab === 'backups' && <ServerBackups s={s} />}
      {tab === 'configuration' && <ServerConfig s={s} />}
      {tab === 'updates' && <ServerUpdates s={s} />}
    </div>
  )
}
