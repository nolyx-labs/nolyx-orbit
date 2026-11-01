import { useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { useCan, useProject } from '../../../components/layout/context'
import { Confirm } from '../../../components/ui/Confirm'
import { Panel } from '../../../components/ui/Panel'
import { Empty, Loading } from '../../../components/ui/states'
import { StatusText } from '../../../components/ui/status'
import { api, useApi } from '../../../lib/api'
import { ago, bytes, datetime } from '../../../lib/format'
import { useLive } from '../../../lib/live'
import { opPath } from '../../../lib/paths'
import { Database, Operation } from '../../../lib/types'
import { OP_LABEL } from '../../operations/OperationPage'
import { BackupConfigDialog } from '../BackupConfigDialog'

export function DBBackups({ d, onChanged }: { d: Database; onChanged: () => void }) {
  const { project } = useProject()
  const canAdmin = useCan('admin')
  const canMember = useCan('member')
  const nav = useNavigate()
  const [backups, , reload] = useApi<any[]>(`/projects/${project.id}/backups?database=${d.id}`)
  const [ops] = useApi<Operation[]>(`/projects/${project.id}/operations?database=${d.id}`)
  const [editing, setEditing] = useState(false)
  const [restore, setRestore] = useState<any>(null)
  useLive(project.id, ['operations'], reload)
  const cfg = d.backup_config
  return (
    <div className="stack">
      <Panel
        title="Schedule"
        actions={
          canAdmin && (
            <button className="btn sm" onClick={() => setEditing(true)}>
              Configure
            </button>
          )
        }
      >
        <div className="panel-body kv">
          <div className="k">Automatic backups</div>
          <div>
            {cfg?.enabled ? (
              <StatusText tone="ok">Every {cfg.interval_hours}h</StatusText>
            ) : (
              <StatusText tone="">Off</StatusText>
            )}
          </div>
          <div className="k">Retention</div>
          <div>{cfg ? `${cfg.retention} backups` : '—'}</div>
          <div className="k">Destination</div>
          <div>
            {cfg?.s3 ? (
              'S3 (and server volume)'
            ) : (
              <span className="muted">
                Server volume <span className="mono">orbit-backups</span> only
              </span>
            )}
          </div>
          <div className="k">Last run</div>
          <div>{cfg?.last_run_at ? ago(cfg.last_run_at) : 'never'}</div>
        </div>
      </Panel>
      <Panel
        title="Backups"
        actions={
          canMember &&
          d.role !== 'replica' && (
            <button
              className="btn sm primary"
              onClick={() =>
                api<{ operation_id: string }>('POST', `/projects/${project.id}/databases/${d.id}/backups`).then((r) =>
                  nav(opPath(project.id, r.operation_id)),
                )
              }
            >
              Back up now
            </button>
          )
        }
      >
        {!backups ? (
          <Loading />
        ) : !backups.length ? (
          <Empty title="No backups yet">Backups are pg_dump custom-format files, restorable with pg_restore.</Empty>
        ) : (
          <table className="t">
            <thead>
              <tr>
                <th>File</th>
                <th>Status</th>
                <th>Location</th>
                <th className="right">Size</th>
                <th className="right">Created</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {backups.map((b) => (
                <tr key={b.id}>
                  <td className="mono">
                    {b.file}
                    {b.scheduled && <span className="faint"> · scheduled</span>}
                  </td>
                  <td>
                    <StatusText tone={b.status === 'succeeded' ? 'ok' : b.status === 'failed' ? 'err' : 'info'}>
                      {b.status}
                    </StatusText>
                  </td>
                  <td className="mono muted" style={{ fontSize: 11 }}>
                    {b.location || '—'}
                  </td>
                  <td className="right mono">{b.size ? bytes(b.size) : '—'}</td>
                  <td className="right muted">{datetime(b.created_at)}</td>
                  <td className="right">
                    {canAdmin && b.status === 'succeeded' && d.role !== 'replica' && (
                      <button className="btn ghost sm" onClick={() => setRestore(b)}>
                        Restore
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
      <Panel title="Recent operations">
        {!ops?.length ? (
          <Empty title="No operations" />
        ) : (
          <div className="list">
            {ops.map((o) => (
              <Link key={o.id} className="list-item" to={opPath(project.id, o.id)}>
                <StatusText tone={o.status === 'succeeded' ? 'ok' : o.status === 'failed' ? 'err' : 'info'}>
                  {OP_LABEL[o.kind] ?? o.kind}
                </StatusText>
                <span className="mono faint">{o.target}</span>
                <span className="spacer" />
                <span className="faint">{ago(o.created_at)}</span>
              </Link>
            ))}
          </div>
        )}
      </Panel>
      {editing && <BackupConfigDialog d={d} onClose={() => setEditing(false)} onSaved={onChanged} />}
      {restore && (
        <Confirm
          title={`Restore ${d.name} from ${restore.file}?`}
          action="Restore"
          danger
          typeToConfirm={d.name}
          onClose={() => setRestore(null)}
          onConfirm={() =>
            api<{ operation_id: string }>(
              'POST',
              `/projects/${project.id}/databases/${d.id}/backups/${restore.id}/restore`,
              { confirm: d.name },
            ).then((r) => nav(opPath(project.id, r.operation_id)))
          }
        >
          Runs <span className="mono">pg_restore --clean</span>: objects in the backup are dropped and recreated. Writes
          made since {datetime(restore.created_at)} to those objects are lost. Replicas follow the restore.
        </Confirm>
      )}
    </div>
  )
}
