import { useState } from 'react'
import { useProject } from '../../components/layout/context'
import { Dialog } from '../../components/ui/Dialog'
import { ErrorNote } from '../../components/ui/states'
import { api } from '../../lib/api'
import {
  COMPONENT_STATUSES,
  ComponentStatus,
  IMPACTS,
  INCIDENT_STATUSES,
  Incident,
  StatusPage,
  toLocalInput,
} from './statusHelpers'

/** Pick the components an incident affects and what each shows while it's open. */
function AffectedComponents({
  page,
  value,
  onChange,
  maintenance,
}: {
  page: StatusPage
  value: Record<string, ComponentStatus>
  onChange: (v: Record<string, ComponentStatus>) => void
  maintenance?: boolean
}) {
  if (!page.components.length) return <span className="hint">This page has no components yet.</span>
  return (
    <table className="t">
      <tbody>
        {page.components.map((c) => {
          const on = c.id in value
          return (
            <tr key={c.id}>
              <td>
                <label className="row" style={{ gap: 8 }}>
                  <input
                    type="checkbox"
                    checked={on}
                    onChange={(e) => {
                      const next = { ...value }
                      if (e.target.checked) next[c.id] = maintenance ? 'under_maintenance' : 'partial_outage'
                      else delete next[c.id]
                      onChange(next)
                    }}
                  />
                  {c.group_name && <span className="faint">{c.group_name} ·</span>} {c.name}
                </label>
              </td>
              <td className="right">
                {on && !maintenance && (
                  <select
                    className="select"
                    style={{ width: 200 }}
                    value={value[c.id]}
                    onChange={(e) => onChange({ ...value, [c.id]: e.target.value as ComponentStatus })}
                  >
                    {COMPONENT_STATUSES.map(([v, l]) => (
                      <option key={v} value={v}>
                        {l}
                      </option>
                    ))}
                  </select>
                )}
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

export function NewIncidentDialog({
  page,
  kind,
  onClose,
  onDone,
}: {
  page: StatusPage
  kind: 'incident' | 'maintenance'
  onClose: () => void
  onDone: () => void
}) {
  const { project } = useProject()
  const start = new Date(Date.now() + 86400000)
  start.setMinutes(0, 0, 0)
  const [f, setF] = useState({
    name: '',
    impact: 'minor',
    status: kind === 'incident' ? 'investigating' : 'scheduled',
    message: '',
    scheduled_for: toLocalInput(start),
    scheduled_until: toLocalInput(new Date(start.getTime() + 3600000)),
    auto_transition: true,
    notify: true,
  })
  const [components, setComponents] = useState<Record<string, ComponentStatus>>({})
  const [error, setError] = useState<string>()
  const maintenance = kind === 'maintenance'
  const save = () =>
    api('POST', `/projects/${project.id}/status-pages/${page.id}/incidents`, {
      kind,
      name: f.name,
      impact: maintenance ? 'maintenance' : f.impact,
      status: f.status,
      message: f.message,
      components,
      notify: f.notify,
      ...(maintenance && {
        scheduled_for: new Date(f.scheduled_for).toISOString(),
        scheduled_until: new Date(f.scheduled_until).toISOString(),
        auto_transition: f.auto_transition,
      }),
    }).then(
      () => {
        onDone()
        onClose()
      },
      (e) => setError(e.message),
    )
  return (
    <Dialog
      title={maintenance ? 'Schedule maintenance' : 'Report an incident'}
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" disabled={!f.name || !f.message} onClick={save}>
            {maintenance ? 'Schedule' : 'Post incident'}
          </button>
        </>
      }
    >
      <label className="field">
        <span>Title</span>
        <input
          className="input"
          value={f.name}
          onChange={(e) => setF({ ...f, name: e.target.value })}
          placeholder={maintenance ? 'Database upgrade' : 'Elevated API error rates'}
          autoFocus
        />
      </label>
      {maintenance ? (
        <div className="grid-2">
          <label className="field">
            <span>Starts</span>
            <input
              className="input"
              type="datetime-local"
              value={f.scheduled_for}
              onChange={(e) => setF({ ...f, scheduled_for: e.target.value })}
            />
          </label>
          <label className="field">
            <span>Ends</span>
            <input
              className="input"
              type="datetime-local"
              value={f.scheduled_until}
              onChange={(e) => setF({ ...f, scheduled_until: e.target.value })}
            />
          </label>
        </div>
      ) : (
        <div className="grid-2">
          <label className="field">
            <span>Status</span>
            <select className="select" value={f.status} onChange={(e) => setF({ ...f, status: e.target.value })}>
              {INCIDENT_STATUSES.incident.map(([v, l]) => (
                <option key={v} value={v}>
                  {l}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>Impact</span>
            <select className="select" value={f.impact} onChange={(e) => setF({ ...f, impact: e.target.value })}>
              {IMPACTS.map(([v, l]) => (
                <option key={v} value={v}>
                  {l}
                </option>
              ))}
            </select>
          </label>
        </div>
      )}
      <label className="field">
        <span>Message</span>
        <textarea
          className="input"
          rows={4}
          value={f.message}
          onChange={(e) => setF({ ...f, message: e.target.value })}
          placeholder={
            maintenance
              ? 'We will upgrade the primary database. Expect up to 5 minutes of read-only time.'
              : 'We are investigating reports of failed API requests.'
          }
        />
      </label>
      <div className="field">
        <span>Affected components</span>
        <AffectedComponents page={page} value={components} onChange={setComponents} maintenance={maintenance} />
      </div>
      <div className="checkbox-row">
        {maintenance && (
          <label>
            <input
              type="checkbox"
              checked={f.auto_transition}
              onChange={(e) => setF({ ...f, auto_transition: e.target.checked })}
            />
            Start and complete automatically on schedule
          </label>
        )}
        <label>
          <input type="checkbox" checked={f.notify} onChange={(e) => setF({ ...f, notify: e.target.checked })} />
          Notify {page.subscribers} subscriber{page.subscribers === 1 ? '' : 's'}
        </label>
      </div>
      {maintenance && (
        <span className="hint">
          While maintenance is in progress, alerts for its servers and monitors are held, and the components show "Under
          maintenance".
        </span>
      )}
      <ErrorNote error={error} />
    </Dialog>
  )
}

export function UpdateDialog({
  page,
  incident,
  onClose,
  onDone,
}: {
  page: StatusPage
  incident: Incident
  onClose: () => void
  onDone: () => void
}) {
  const { project } = useProject()
  const statuses = INCIDENT_STATUSES[incident.kind]
  const next = statuses[Math.min(statuses.findIndex(([v]) => v === incident.status) + 1, statuses.length - 1)][0]
  const [status, setStatus] = useState<string>(next)
  const [message, setMessage] = useState('')
  const [components, setComponents] = useState<Record<string, ComponentStatus>>(incident.components)
  const [notify, setNotify] = useState(true)
  const [error, setError] = useState<string>()
  return (
    <Dialog
      title={`Update: ${incident.name}`}
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            disabled={!message}
            onClick={() =>
              api('POST', `/projects/${project.id}/status-pages/${page.id}/incidents/${incident.id}/updates`, {
                status,
                message,
                components,
                notify,
              }).then(
                () => {
                  onDone()
                  onClose()
                },
                (e) => setError(e.message),
              )
            }
          >
            Post update
          </button>
        </>
      }
    >
      <label className="field">
        <span>Status</span>
        <select className="select" value={status} onChange={(e) => setStatus(e.target.value)}>
          {statuses.map(([v, l]) => (
            <option key={v} value={v}>
              {l}
            </option>
          ))}
        </select>
      </label>
      <label className="field">
        <span>Message</span>
        <textarea className="input" rows={4} value={message} onChange={(e) => setMessage(e.target.value)} autoFocus />
      </label>
      {status !== 'resolved' && status !== 'completed' && (
        <div className="field">
          <span>Affected components</span>
          <AffectedComponents
            page={page}
            value={components}
            onChange={setComponents}
            maintenance={incident.kind === 'maintenance'}
          />
        </div>
      )}
      <div className="checkbox-row">
        <label>
          <input type="checkbox" checked={notify} onChange={(e) => setNotify(e.target.checked)} />
          Notify {page.subscribers} subscriber{page.subscribers === 1 ? '' : 's'}
        </label>
      </div>
      <ErrorNote error={error} />
    </Dialog>
  )
}
