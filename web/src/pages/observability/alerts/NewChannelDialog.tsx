import { useState } from 'react'
import { useProject } from '../../../components/layout/context'
import { Dialog } from '../../../components/ui/Dialog'
import { ErrorNote } from '../../../components/ui/states'
import { api } from '../../../lib/api'

export function NewChannel({
  emailConfigured,
  onClose,
  onSaved,
}: {
  emailConfigured: boolean
  onClose: () => void
  onSaved: () => void
}) {
  const { project } = useProject()
  const [f, setF] = useState({ name: '', type: 'slack', url: '', to: '' })
  const [error, setError] = useState<string>()
  const save = (test: boolean) =>
    api('POST', `/projects/${project.id}/channels`, {
      name: f.name,
      type: f.type,
      url: f.url,
      to: f.to.split(/[\s,]+/).filter(Boolean),
      test,
    }).then(
      () => {
        onSaved()
        onClose()
      },
      (e) => setError(e.message),
    )
  return (
    <Dialog
      title="Add notification channel"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn" onClick={() => save(false)}>
            Save
          </button>
          <button className="btn primary" onClick={() => save(true)}>
            Send test &amp; save
          </button>
        </>
      }
    >
      <div className="grid-2">
        <label className="field">
          <span>Name</span>
          <input
            className="input"
            placeholder="#ops"
            value={f.name}
            onChange={(e) => setF({ ...f, name: e.target.value })}
            autoFocus
          />
        </label>
        <label className="field">
          <span>Type</span>
          <select className="select" value={f.type} onChange={(e) => setF({ ...f, type: e.target.value })}>
            <option value="slack">Slack</option>
            <option value="discord">Discord</option>
            <option value="webhook">Webhook</option>
            <option value="email" disabled={!emailConfigured}>
              Email{!emailConfigured ? ' (SMTP not configured)' : ''}
            </option>
          </select>
        </label>
      </div>
      {f.type === 'email' ? (
        <label className="field">
          <span>Recipients</span>
          <input
            className="input"
            placeholder="ops@company.com, oncall@company.com"
            value={f.to}
            onChange={(e) => setF({ ...f, to: e.target.value })}
          />
        </label>
      ) : (
        <label className="field">
          <span>{f.type === 'webhook' ? 'URL' : 'Incoming webhook URL'}</span>
          <input
            className="input mono"
            placeholder={
              f.type === 'slack'
                ? 'https://hooks.slack.com/services/…'
                : f.type === 'discord'
                  ? 'https://discord.com/api/webhooks/…'
                  : 'https://…'
            }
            value={f.url}
            onChange={(e) => setF({ ...f, url: e.target.value })}
          />
          {f.type === 'webhook' && (
            <span className="hint">POSTs JSON with status, rule, metric, value, threshold and target.</span>
          )}
        </label>
      )}
      <ErrorNote error={error} />
    </Dialog>
  )
}
