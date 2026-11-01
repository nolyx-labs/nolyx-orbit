import { useState } from 'react'
import { useProject } from '../../components/layout/context'
import { Dialog } from '../../components/ui/Dialog'
import { ErrorNote } from '../../components/ui/states'
import { api } from '../../lib/api'
import { ServerSelect } from './ServerSelect'

export function ConnectDialog({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { project } = useProject()
  const [f, setF] = useState({
    name: '',
    server_id: '',
    host: '127.0.0.1',
    port: '5432',
    username: 'postgres',
    password: '',
    dbname: 'postgres',
    sslmode: 'prefer',
    replication_password: '',
  })
  const [error, setError] = useState<string>()
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setF({ ...f, [k]: e.target.value })
  const save = () =>
    api('POST', `/projects/${project.id}/databases`, { ...f, port: +f.port }).then(
      () => {
        onDone()
        onClose()
      },
      (e) => setError(e.message),
    )
  return (
    <Dialog
      title="Connect existing PostgreSQL"
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" onClick={save}>
            Connect
          </button>
        </>
      }
    >
      <div className="muted">
        The agent on the chosen server connects to the database and samples it every 15 seconds. Use a role with{' '}
        <span className="mono">pg_monitor</span>. Credentials are encrypted at rest and only sent to that agent.
      </div>
      <div className="grid-2">
        <label className="field">
          <span>Name</span>
          <input className="input mono" placeholder="production-db" value={f.name} onChange={set('name')} autoFocus />
        </label>
        <label className="field">
          <span>Monitored from server</span>
          <ServerSelect value={f.server_id} onChange={(v) => setF((x) => ({ ...x, server_id: v }))} />
        </label>
        <label className="field">
          <span>Host</span>
          <input className="input mono" value={f.host} onChange={set('host')} />
          <span className="hint">As seen from that server.</span>
        </label>
        <label className="field">
          <span>Port</span>
          <input className="input mono" value={f.port} onChange={set('port')} />
        </label>
        <label className="field">
          <span>User</span>
          <input className="input mono" value={f.username} onChange={set('username')} />
        </label>
        <label className="field">
          <span>Password</span>
          <input
            className="input mono"
            type="password"
            value={f.password}
            onChange={set('password')}
            autoComplete="new-password"
          />
        </label>
        <label className="field">
          <span>Database</span>
          <input className="input mono" value={f.dbname} onChange={set('dbname')} />
        </label>
        <label className="field">
          <span>SSL mode</span>
          <select className="select" value={f.sslmode} onChange={set('sslmode')}>
            {['disable', 'prefer', 'require', 'verify-ca', 'verify-full'].map((m) => (
              <option key={m}>{m}</option>
            ))}
          </select>
        </label>
      </div>
      <label className="field">
        <span>Replication password (optional)</span>
        <input
          className="input mono"
          type="password"
          value={f.replication_password}
          onChange={set('replication_password')}
          autoComplete="new-password"
        />
        <span className="hint">
          To let Orbit create replicas of this primary: create a role{' '}
          <span className="mono">orbit_replicator WITH REPLICATION LOGIN</span>, allow it in pg_hba.conf, and enter its
          password.
        </span>
      </label>
      <ErrorNote error={error} />
    </Dialog>
  )
}
