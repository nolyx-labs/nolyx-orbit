import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useProject } from '../../components/layout/context'
import { Dialog } from '../../components/ui/Dialog'
import { ErrorNote } from '../../components/ui/states'
import { api } from '../../lib/api'
import { opPath } from '../../lib/paths'
import { Database } from '../../lib/types'
import { ServerSelect } from './ServerSelect'

export function ReplicaDialog({ primary, onClose }: { primary: Database; onClose: () => void }) {
  const { project } = useProject()
  const nav = useNavigate()
  const [f, setF] = useState({
    name: `${primary.name}-replica`,
    server_id: '',
    port: String(primary.port + 1),
    cpus: '',
    memory_mb: '',
  })
  const [error, setError] = useState<string>()
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement>) => setF({ ...f, [k]: e.target.value })
  const save = () =>
    api<{ operation_id: string }>('POST', `/projects/${project.id}/databases/${primary.id}/replicas`, {
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
      title="Create PostgreSQL replica"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" onClick={save}>
            Create replica
          </button>
        </>
      }
    >
      <div className="kv">
        <div className="k">Primary</div>
        <div className="mono">{primary.name}</div>
        <div className="k">Upstream</div>
        <div className="mono">
          {primary.server_ip}:{primary.port}
        </div>
        <div className="k">Encryption</div>
        <div className="muted">
          Replication over the network uses PostgreSQL's SSL settings; data at rest uses the target disk's encryption.
        </div>
      </div>
      <div className="grid-2">
        <label className="field">
          <span>Name</span>
          <input className="input mono" value={f.name} onChange={set('name')} />
        </label>
        <label className="field">
          <span>Target server</span>
          <ServerSelect value={f.server_id} onChange={(v) => setF((x) => ({ ...x, server_id: v }))} />
        </label>
        <label className="field">
          <span>Host port</span>
          <input className="input mono" value={f.port} onChange={set('port')} />
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
      <div className="muted" style={{ fontSize: 12 }}>
        The target server must reach the primary on {primary.server_ip}:{primary.port}. The copy streams with
        pg_basebackup and the replica then follows the primary as a hot standby.
      </div>
      <ErrorNote error={error} />
    </Dialog>
  )
}
