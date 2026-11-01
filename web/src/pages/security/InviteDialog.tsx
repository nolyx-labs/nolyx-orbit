import { useState } from 'react'
import { useProject } from '../../components/layout/context'
import { Dialog } from '../../components/ui/Dialog'
import { Copy } from '../../components/ui/controls'
import { ErrorNote } from '../../components/ui/states'
import { api } from '../../lib/api'

export function InviteDialog({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { org } = useProject()
  const [f, setF] = useState({ email: '', role: 'member' })
  const [res, setRes] = useState<{ link: string; emailed: boolean }>()
  const [error, setError] = useState<string>()
  const save = () =>
    api<{ link: string; emailed: boolean }>('POST', `/orgs/${org.id}/invites`, f).then(
      (r) => {
        setRes(r)
        onDone()
      },
      (e) => setError(e.message),
    )
  if (res)
    return (
      <Dialog
        title="Invitation created"
        onClose={onClose}
        footer={
          <button className="btn primary" onClick={onClose}>
            Done
          </button>
        }
      >
        <div className="muted">
          {res.emailed
            ? `We emailed ${f.email}. You can also share this link:`
            : 'Email is not configured on this instance. Send this link yourself:'}
        </div>
        <div className="copy-row">
          <pre className="code">{res.link}</pre>
          <Copy text={res.link} />
        </div>
        <div className="faint" style={{ fontSize: 12 }}>
          The link expires in 7 days and only works for {f.email}.
        </div>
      </Dialog>
    )
  return (
    <Dialog
      title="Invite member"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" disabled={!f.email} onClick={save}>
            Invite
          </button>
        </>
      }
    >
      <label className="field">
        <span>Email</span>
        <input
          className="input"
          type="email"
          value={f.email}
          onChange={(e) => setF({ ...f, email: e.target.value })}
          autoFocus
        />
      </label>
      <label className="field">
        <span>Role</span>
        <select className="select" value={f.role} onChange={(e) => setF({ ...f, role: e.target.value })}>
          <option>viewer</option>
          <option>member</option>
          <option>admin</option>
        </select>
      </label>
      <ErrorNote error={error} />
    </Dialog>
  )
}
