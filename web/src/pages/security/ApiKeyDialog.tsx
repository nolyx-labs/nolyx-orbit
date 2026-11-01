import { useState } from 'react'
import { useProject } from '../../components/layout/context'
import { Dialog } from '../../components/ui/Dialog'
import { Copy } from '../../components/ui/controls'
import { ErrorNote } from '../../components/ui/states'
import { api } from '../../lib/api'

export function KeyDialog({
  projects,
  onClose,
  onSaved,
}: {
  projects: { id: string; name: string }[]
  onClose: () => void
  onSaved: () => void
}) {
  const { org } = useProject()
  const [f, setF] = useState({ name: '', role: 'viewer', project_id: '', expires_days: '90' })
  const [token, setToken] = useState<string>()
  const [error, setError] = useState<string>()
  const save = () =>
    api<{ token: string }>('POST', `/orgs/${org.id}/api-keys`, { ...f, expires_days: +f.expires_days }).then(
      (r) => {
        setToken(r.token)
        onSaved()
      },
      (e) => setError(e.message),
    )
  if (token)
    return (
      <Dialog
        title="API key created"
        onClose={onClose}
        footer={
          <button className="btn primary" onClick={onClose}>
            Done
          </button>
        }
      >
        <div className="callout warn">Copy this key now. It is shown only once.</div>
        <div className="copy-row">
          <pre className="code">{token}</pre>
          <Copy text={token} />
        </div>
      </Dialog>
    )
  return (
    <Dialog
      title="Create API key"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" disabled={!f.name} onClick={save}>
            Create
          </button>
        </>
      }
    >
      <label className="field">
        <span>Name</span>
        <input
          className="input"
          placeholder="GitHub Actions"
          value={f.name}
          onChange={(e) => setF({ ...f, name: e.target.value })}
          autoFocus
        />
      </label>
      <div className="grid-2">
        <label className="field">
          <span>Role</span>
          <select className="select" value={f.role} onChange={(e) => setF({ ...f, role: e.target.value })}>
            <option value="viewer">viewer — read only</option>
            <option value="member">member — deploy &amp; operate</option>
            <option value="admin">admin</option>
          </select>
        </label>
        <label className="field">
          <span>Expires in (days, 0 = never)</span>
          <input
            className="input mono"
            value={f.expires_days}
            onChange={(e) => setF({ ...f, expires_days: e.target.value })}
          />
        </label>
      </div>
      <label className="field">
        <span>Project</span>
        <select className="select" value={f.project_id} onChange={(e) => setF({ ...f, project_id: e.target.value })}>
          <option value="">All projects</option>
          {projects.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </label>
      <ErrorNote error={error} />
    </Dialog>
  )
}
