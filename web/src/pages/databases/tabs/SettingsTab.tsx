import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useCan, useProject } from '../../../components/layout/context'
import { Confirm } from '../../../components/ui/Confirm'
import { Panel } from '../../../components/ui/Panel'
import { api } from '../../../lib/api'
import { opPath } from '../../../lib/paths'
import { Database } from '../../../lib/types'

export function DBSettings({ d }: { d: Database }) {
  const { project } = useProject()
  const canAdmin = useCan('admin')
  const nav = useNavigate()
  const [confirm, setConfirm] = useState(false)
  return (
    <div className="stack">
      <Panel title="Connection">
        <div className="panel-body kv">
          <div className="k">Host</div>
          <div className="mono">
            {d.host}:{d.port}
          </div>
          <div className="k">From other servers</div>
          <div className="mono">
            {d.server_ip}:{d.port}
          </div>
          <div className="k">User</div>
          <div className="mono">{d.username}</div>
          <div className="k">Database</div>
          <div className="mono">{d.dbname}</div>
          <div className="k">SSL mode</div>
          <div className="mono">{d.sslmode}</div>
          <div className="k">Monitored by</div>
          <div>
            <Link to={`/p/${project.id}/servers/${d.server_id}/overview`}>{d.server_name}</Link>
          </div>
          <div className="k">System identifier</div>
          <div className="mono">{d.system_id || '—'}</div>
        </div>
      </Panel>
      {canAdmin && (
        <Panel title="Danger zone">
          <div className="panel-body row">
            <div style={{ flex: 1 }}>
              <div style={{ fontWeight: 500 }}>{d.managed ? 'Remove database' : 'Disconnect database'}</div>
              <div className="muted">
                {d.managed
                  ? 'Stops the container and deletes its data volume. Backups on the volume orbit-backups and in S3 are kept.'
                  : 'Stops monitoring. The database itself is not touched.'}
              </div>
            </div>
            <button className="btn danger" onClick={() => setConfirm(true)}>
              {d.managed ? 'Remove' : 'Disconnect'}
            </button>
          </div>
        </Panel>
      )}
      {confirm && (
        <Confirm
          title={`${d.managed ? 'Remove' : 'Disconnect'} ${d.name}?`}
          action={d.managed ? 'Remove database' : 'Disconnect'}
          danger
          typeToConfirm={d.name}
          onClose={() => setConfirm(false)}
          onConfirm={() =>
            api<{ operation_id?: string }>('DELETE', `/projects/${project.id}/databases/${d.id}`, {
              confirm: d.name,
            }).then((r) => nav(r?.operation_id ? opPath(project.id, r.operation_id) : `/p/${project.id}/databases`))
          }
        >
          {d.managed ? (
            <>
              All data in <span className="mono">{d.name}</span> is permanently deleted. Take a backup first if you
              might need it.
            </>
          ) : (
            'Orbit stops collecting metrics for this database.'
          )}
        </Confirm>
      )}
    </div>
  )
}
