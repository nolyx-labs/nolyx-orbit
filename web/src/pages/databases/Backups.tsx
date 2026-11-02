import { Link } from 'react-router'
import { useProject } from '../../components/layout/context'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { useApi } from '../../lib/api'
import { bytes, datetime } from '../../lib/format'
import { useLive } from '../../lib/live'

interface ServerBackupRow {
  id: string
  name: string
  schedule: string | null
  destination: 's3' | 'server' | null
  s3_provider: string | null
  target_name: string | null
  last_backup_at: string | null
  last_run_status: string | null
}

export function Backups() {
  const { project } = useProject()
  const [backups, , reload] = useApi<any[]>(`/projects/${project.id}/backups`)
  const [servers, , reloadServers] = useApi<{ servers: ServerBackupRow[] }>(`/projects/${project.id}/server-backups`)
  useLive(project.id, ['operations'], reload)
  useLive(project.id, ['server-backups', 'operations'], reloadServers)
  return (
    <div className="page">
      <PageHead title="Backups" sub="Server backups of every service and database, and pg_dump backups per database" />
      <div className="stack">
        <Panel title="Servers">
          {!servers ? (
            <Loading />
          ) : !servers.servers.length ? (
            <Empty title="No servers" />
          ) : (
            <table className="t">
              <thead>
                <tr>
                  <th>Server</th>
                  <th>Destination</th>
                  <th>Schedule</th>
                  <th>Last run</th>
                  <th className="right">Last good backup</th>
                </tr>
              </thead>
              <tbody>
                {servers.servers.map((s) => (
                  <tr key={s.id}>
                    <td>
                      <Link to={`/p/${project.id}/servers/${s.id}/backups`}>{s.name}</Link>
                    </td>
                    <td className="muted">
                      {!s.destination
                        ? 'Not set up'
                        : s.destination === 's3'
                          ? `S3 (${s.s3_provider})`
                          : `Server ${s.target_name ?? ''}`}
                    </td>
                    <td className="muted">{s.schedule ?? '—'}</td>
                    <td>
                      {s.last_run_status ? (
                        <StatusText
                          tone={
                            s.last_run_status === 'succeeded' ? 'ok' : s.last_run_status === 'failed' ? 'err' : 'info'
                          }
                        >
                          {s.last_run_status}
                        </StatusText>
                      ) : (
                        <span className="faint">—</span>
                      )}
                    </td>
                    <td className="right muted">{s.last_backup_at ? datetime(s.last_backup_at) : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
        <Panel title="Databases">
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
    </div>
  )
}
