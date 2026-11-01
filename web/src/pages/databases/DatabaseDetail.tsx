import { Link, NavLink, useParams } from 'react-router-dom'
import { useProject } from '../../components/layout/context'
import { PageHead } from '../../components/ui/PageHead'
import { Empty, Loading } from '../../components/ui/states'
import { Badge, StatusText } from '../../components/ui/status'
import { useApi } from '../../lib/api'
import { useLive } from '../../lib/live'
import { Database } from '../../lib/types'
import { dbTone, likelyCause } from './dbHelpers'
import { DBBackups } from './tabs/BackupsTab'
import { DBOverview } from './tabs/OverviewTab'
import { DBQueries } from './tabs/QueriesTab'
import { DBReplication } from './tabs/ReplicationTab'
import { DBSettings } from './tabs/SettingsTab'
import { DBTables } from './tabs/TablesTab'

const DATABASE_TABS = ['overview', 'queries', 'tables', 'replication', 'backups', 'settings'] as const

export function DatabaseDetail() {
  const { project } = useProject()
  const { id, tab = 'overview' } = useParams()
  const [d, error, reload] = useApi<Database>(`/projects/${project.id}/databases/${id}`)
  useLive(project.id, ['databases', 'operations'], reload, 3000)
  if (error)
    return (
      <div className="page">
        <Empty title={error} />
      </div>
    )
  if (!d)
    return (
      <div className="page">
        <Loading />
      </div>
    )
  const base = `/p/${project.id}/databases/${d.id}`
  const cause = d.status !== 'healthy' ? likelyCause(d) : null
  return (
    <div className="page">
      <PageHead
        crumbs={<Link to={`/p/${project.id}/databases`}>Databases</Link>}
        title={d.name}
        sub={
          <span className="row" style={{ gap: 10 }}>
            <StatusText tone={dbTone(d)}>{d.status[0].toUpperCase() + d.status.slice(1)}</StatusText>
            <span className="faint">·</span>
            <span className="muted">{d.role}</span>
            <span className="faint">·</span>
            <span className="mono muted">
              {d.host}:{d.port}
            </span>
            <span className="faint">·</span>
            <span className="muted">PostgreSQL {d.version.split(' ')[0] || '—'}</span>
            {d.managed && <Badge>managed</Badge>}
          </span>
        }
      />
      {d.stats.issues?.length || cause || d.error ? (
        <div
          className={`callout ${d.status === 'down' || d.status === 'failed' ? 'err' : 'warn'}`}
          style={{ marginBottom: 16 }}
        >
          {d.stats.issues?.map((i) => (
            <div key={i}>
              <b style={{ fontWeight: 500 }}>{i}</b>
            </div>
          ))}
          {d.status === 'failed' && d.error && (
            <div className="mono" style={{ whiteSpace: 'pre-wrap' }}>
              {d.error}
            </div>
          )}
          {cause && (
            <div className="muted" style={{ marginTop: 4 }}>
              Likely cause: {cause}
            </div>
          )}
          <div className="row" style={{ marginTop: 8 }}>
            <Link className="btn sm" to={`/p/${project.id}/servers/${d.server_id}/overview`}>
              View server
            </Link>
            <Link className="btn sm" to={`/p/${project.id}/servers/${d.server_id}/metrics`}>
              View metrics
            </Link>
            <Link
              className="btn sm"
              to={`/p/${project.id}/logs?server=${d.server_id}${d.managed ? `&container=orbit-pg-${d.name}` : ''}`}
            >
              View logs
            </Link>
          </div>
        </div>
      ) : null}
      <div className="tabs">
        {DATABASE_TABS.map((t) => (
          <NavLink key={t} to={`${base}/${t}`} className={t === tab ? 'active' : ''}>
            {t[0].toUpperCase() + t.slice(1)}
          </NavLink>
        ))}
      </div>
      {tab === 'overview' && <DBOverview d={d} />}
      {tab === 'queries' && <DBQueries d={d} />}
      {tab === 'tables' && <DBTables d={d} />}
      {tab === 'replication' && <DBReplication d={d} />}
      {tab === 'backups' && <DBBackups d={d} onChanged={reload} />}
      {tab === 'settings' && <DBSettings d={d} />}
    </div>
  )
}
