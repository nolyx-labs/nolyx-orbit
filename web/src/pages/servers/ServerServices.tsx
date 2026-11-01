import { Link } from 'react-router-dom'
import { useProject } from '../../components/layout/context'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { useApi } from '../../lib/api'

export function ServerServices({ sid }: { sid: string }) {
  const { project } = useProject()
  const [apps] = useApi<any[]>(`/projects/${project.id}/apps`)
  const mine = apps?.filter((a) => a.server_id === sid)
  return (
    <Panel>
      {!mine ? (
        <Loading />
      ) : !mine.length ? (
        <Empty title="No services target this server" />
      ) : (
        <div className="list">
          {mine.map((a) => (
            <Link key={a.id} className="list-item" to={`/p/${project.id}/services/${a.id}`}>
              <span style={{ fontWeight: 500, width: 160 }}>{a.name}</span>
              <span className="mono muted" style={{ flex: 1 }}>
                {a.image}
              </span>
              <StatusText tone={a.container?.state === 'running' ? 'ok' : 'warn'}>
                {a.container?.state ?? 'not deployed'}
              </StatusText>
            </Link>
          ))}
        </div>
      )}
    </Panel>
  )
}
