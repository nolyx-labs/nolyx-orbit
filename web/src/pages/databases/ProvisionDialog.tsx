import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useProject } from '../../components/layout/context'
import { Dialog } from '../../components/ui/Dialog'
import { ErrorNote } from '../../components/ui/states'
import { api } from '../../lib/api'
import { opPath } from '../../lib/paths'
import { ServerSelect } from './ServerSelect'

export function ProvisionDialog({ onClose }: { onClose: () => void }) {
  const { project } = useProject()
  const nav = useNavigate()
  const [f, setF] = useState({
    name: '',
    server_id: '',
    version: '17',
    port: '5432',
    dbname: 'app',
    cpus: '',
    memory_mb: '',
  })
  const [error, setError] = useState<string>()
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setF({ ...f, [k]: e.target.value })
  const save = () =>
    api<{ operation_id: string }>('POST', `/projects/${project.id}/databases/provision`, {
      ...f,
      port: +f.port,
      cpus: +f.cpus || 0,
      memory_mb: +f.memory_mb || 0,
    }).then(
      (r) => nav(opPath(project.id, r.operation_id)),
      (e) => setError(e.message),
    )
  return (
    <Dialog
      title="Create PostgreSQL"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" onClick={save}>
            Create database
          </button>
        </>
      }
    >
      <div className="muted">
        Runs the official <span className="mono">postgres</span> image with a persistent volume, replication enabled and
        pg_stat_statements loaded. Passwords are generated and encrypted.
      </div>
      <div className="grid-2">
        <label className="field">
          <span>Name</span>
          <input className="input mono" placeholder="main-db" value={f.name} onChange={set('name')} autoFocus />
        </label>
        <label className="field">
          <span>Server</span>
          <ServerSelect value={f.server_id} onChange={(v) => setF((x) => ({ ...x, server_id: v }))} />
        </label>
        <label className="field">
          <span>Version</span>
          <select className="select" value={f.version} onChange={set('version')}>
            {['17', '16', '15'].map((v) => (
              <option key={v}>{v}</option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>Host port</span>
          <input className="input mono" value={f.port} onChange={set('port')} />
        </label>
        <label className="field">
          <span>Database</span>
          <input className="input mono" value={f.dbname} onChange={set('dbname')} />
        </label>
        <div />
        <label className="field">
          <span>CPU limit</span>
          <input className="input mono" placeholder="unlimited" value={f.cpus} onChange={set('cpus')} />
        </label>
        <label className="field">
          <span>Memory limit (MB)</span>
          <input className="input mono" placeholder="unlimited" value={f.memory_mb} onChange={set('memory_mb')} />
        </label>
      </div>
      <div className="callout warn">
        The port is published on the server's public interfaces. Restrict it with a firewall to your application
        servers.
      </div>
      <ErrorNote error={error} />
    </Dialog>
  )
}
