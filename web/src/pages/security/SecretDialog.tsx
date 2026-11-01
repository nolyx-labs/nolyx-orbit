import { useState } from 'react'
import { useCan, useProject } from '../../components/layout/context'
import { Dialog } from '../../components/ui/Dialog'
import { ErrorNote } from '../../components/ui/states'
import { api } from '../../lib/api'

export function SecretDialog({
  initial,
  onClose,
  onSaved,
}: {
  initial: { key: string; scope: string }
  onClose: () => void
  onSaved: () => void
}) {
  const { project } = useProject()
  const canOrg = useCan('admin')
  const [f, setF] = useState({ ...initial, value: '' })
  const [error, setError] = useState<string>()
  const save = () =>
    api('PUT', `/projects/${project.id}/secrets`, f).then(
      () => {
        onSaved()
        onClose()
      },
      (e) => setError(e.message),
    )
  return (
    <Dialog
      title={initial.key ? `Replace ${initial.key}` : 'Add secret'}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" disabled={!f.key} onClick={save}>
            Save
          </button>
        </>
      }
    >
      <label className="field">
        <span>Name</span>
        <input
          className="input mono"
          value={f.key}
          disabled={!!initial.key}
          placeholder="STRIPE_SECRET_KEY"
          onChange={(e) => setF({ ...f, key: e.target.value.toUpperCase() })}
          autoFocus={!initial.key}
        />
      </label>
      <label className="field">
        <span>Value</span>
        <textarea
          className="input"
          rows={3}
          value={f.value}
          onChange={(e) => setF({ ...f, value: e.target.value })}
          autoFocus={!!initial.key}
          spellCheck={false}
        />
      </label>
      <label className="field">
        <span>Scope</span>
        <select
          className="select"
          value={f.scope}
          disabled={!!initial.key}
          onChange={(e) => setF({ ...f, scope: e.target.value })}
        >
          <option value="project">This project ({project.name})</option>
          {canOrg && <option value="organization">Whole organization</option>}
        </select>
      </label>
      <ErrorNote error={error} />
    </Dialog>
  )
}
