import { Link } from 'react-router-dom'
import { useProject } from '../../components/layout/context'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { useApi } from '../../lib/api'
import { bytes, datetime } from '../../lib/format'
import { useLive } from '../../lib/live'

export function Backups() {
  const { project } = useProject()
  const [backups, , reload] = useApi<any[]>(`/projects/${project.id}/backups`)
  useLive(project.id, ['operations'], reload)
  return (
    <div className="page">
      <PageHead title="Backups" sub="pg_dump backups for all databases. Configure schedules and S3 per database." />
      <Panel>
        {!backups ? (
          <Loading />
        ) : !backups.length ? (
          <Empty title="No backups yet">Open a database → Backups to back up now or set a schedule.</Empty>
        ) : (
          <table className="t">
            <thead>
              <tr>
                <th>Database</th>
                <th>File</th>
                <th>Status</th>
                <th>Location</th>
                <th className="right">Size</th>
                <th className="right">Created</th>
              </tr>
            </thead>
            <tbody>
              {backups.map((b) => (
                <tr key={b.id}>
                  <td>
                    <Link to={`/p/${project.id}/databases/${b.database_id}/backups`}>{b.database}</Link>
                  </td>
                  <td className="mono">{b.file}</td>
                  <td>
                    <StatusText tone={b.status === 'succeeded' ? 'ok' : b.status === 'failed' ? 'err' : 'info'}>
                      {b.status}
                      {b.scheduled ? ' · scheduled' : ''}
                    </StatusText>
                  </td>
                  <td className="mono muted" style={{ fontSize: 11 }}>
                    {b.location || '—'}
                  </td>
                  <td className="right mono">{b.size ? bytes(b.size) : '—'}</td>
                  <td className="right muted">{datetime(b.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </div>
  )
}
