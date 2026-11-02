import { useState } from 'react'
import { useCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { Dialog } from '../../components/ui/Dialog'
import { Panel } from '../../components/ui/Panel'
import { ErrorNote } from '../../components/ui/states'
import { Badge } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { datetime } from '../../lib/format'
import { useLive } from '../../lib/live'
import { Server } from '../../lib/types'
import { toLocalInput } from '../status/statusHelpers'

interface Window {
  id: string
  starts_at: string
  ends_at: string
  reason: string
  action: 'none' | 'restart' | 'update'
  status: 'planned' | 'active' | 'done' | 'cancelled'
  result: string
  status_pages: number
  created_by: string | null
}

interface Maintenance {
  windows: Window[]
  quiet_hour: { hour: number; cpu: number } | null
}

const ACTIONS = {
  none: 'Nothing',
  restart: 'Restart the server',
  update: 'Install all waiting updates (restart if needed)',
}

const DURATIONS = [30, 60, 120, 240, 480, 720, 1440]

const useMaintenance = (sid: string) => {
  const { project } = useProject()
  const res = useApi<Maintenance>(`/projects/${project.id}/servers/${sid}/maintenance`)
  useLive(project.id, ['maintenance'], res[2])
  return res
}

/** The server header's "Maintenance" action. */
export function MaintenanceButton({ s }: { s: Server }) {
  const [open, setOpen] = useState(false)
  if (!useCan('member')) return null
  return (
    <>
      <button className="btn" onClick={() => setOpen(true)}>
        Maintenance
      </button>
      {open && <MaintenanceDialog s={s} onClose={() => setOpen(false)} />}
    </>
  )
}

function MaintenanceDialog({ s, onClose }: { s: Server; onClose: () => void }) {
  const { project } = useProject()
  const [data] = useMaintenance(s.id)
  const [later, setLater] = useState('')
  const [minutes, setMinutes] = useState(60)
  const [reason, setReason] = useState('')
  const [action, setAction] = useState<keyof typeof ACTIONS>('none')
  const [statusPages, setStatusPages] = useState(false)
  const [error, setError] = useState<string>()
  const now = new Date()
  const quiet = data?.quiet_hour
  // The next time the quiet hour (UTC) comes round, at least an hour from now.
  const nextQuiet = () => {
    const d = new Date(now)
    d.setUTCHours(quiet!.hour, 0, 0, 0)
    while (d.getTime() < now.getTime() + 3600000) d.setUTCDate(d.getUTCDate() + 1)
    return toLocalInput(d)
  }
  const save = () =>
    api('POST', `/projects/${project.id}/servers/${s.id}/maintenance`, {
      starts_at: later ? new Date(later).toISOString() : undefined,
      minutes,
      reason,
      action,
      status_pages: statusPages,
    }).then(onClose, (e) => setError(e.message))
  return (
    <Dialog
      title={`Maintenance on ${s.name}`}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" onClick={save}>
            {later ? 'Plan maintenance' : 'Start maintenance now'}
          </button>
        </>
      }
    >
      <div className="muted">
        While maintenance is on, alerts for this server and its monitors are held, and status pages show its components
        as under maintenance.
      </div>
      <div className="checkbox-row">
        <label>
          <input type="radio" checked={!later} onChange={() => setLater('')} />
          Start now
        </label>
        <label>
          <input
            type="radio"
            checked={!!later}
            onChange={() => setLater(quiet ? nextQuiet() : toLocalInput(new Date(now.getTime() + 3600000)))}
          />
          Plan for later
        </label>
      </div>
      {later && (
        <label className="field">
          <span>Starts</span>
          <input
            className="input"
            type="datetime-local"
            value={later}
            min={toLocalInput(now)}
            max={toLocalInput(new Date(now.getTime() + 60 * 86400000))}
            onChange={(e) => setLater(e.target.value)}
          />
          {quiet && (
            <span className="hint">
              Quietest hour over the last two weeks:{' '}
              {new Date(Date.UTC(2000, 0, 1, quiet.hour)).toLocaleTimeString([], {
                hour: '2-digit',
                minute: '2-digit',
              })}{' '}
              (average CPU {quiet.cpu}%).{' '}
              <button className="btn sm ghost" onClick={() => setLater(nextQuiet())}>
                Use it
              </button>
            </span>
          )}
        </label>
      )}
      <div className="grid-2">
        <label className="field">
          <span>Duration</span>
          <select className="select" value={minutes} onChange={(e) => setMinutes(+e.target.value)}>
            {DURATIONS.map((m) => (
              <option key={m} value={m}>
                {m < 60 ? `${m} minutes` : m === 60 ? '1 hour' : `${m / 60} hours`}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>When it starts</span>
          <select className="select" value={action} onChange={(e) => setAction(e.target.value as typeof action)}>
            {Object.entries(ACTIONS).map(([k, v]) => (
              <option key={k} value={k}>
                {v}
              </option>
            ))}
          </select>
        </label>
      </div>
      {action !== 'none' && (
        <span className="hint">Maintenance ends early once the server is back and the work is done.</span>
      )}
      <label className="field">
        <span>Reason</span>
        <input
          className="input"
          maxLength={200}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder="Upgrading the kernel"
        />
      </label>
      <div className="checkbox-row">
        <label>
          <input type="checkbox" checked={statusPages} onChange={(e) => setStatusPages(e.target.checked)} />
          Show on status pages (with the reason as the message)
        </label>
      </div>
      <ErrorNote error={error} />
    </Dialog>
  )
}

/** Active and planned windows, for the server overview. */
export function MaintenanceWindows({ s }: { s: Server }) {
  const { project } = useProject()
  const canEdit = useCan('member')
  const [data, , reload] = useMaintenance(s.id)
  const [ending, setEnding] = useState<Window | null>(null)
  const open = data?.windows.filter((w) => w.status === 'planned' || w.status === 'active') ?? []
  if (!open.length) return null
  return (
    <div style={{ marginBottom: 16 }}>
      <Panel title="Maintenance">
        <div className="list">
          {open.map((w) => (
            <div key={w.id} className="list-item">
              <Badge tone={w.status === 'active' ? 'info' : ''}>
                {w.status === 'active' ? 'in progress' : 'planned'}
              </Badge>
              <span style={{ flex: 1, minWidth: 0 }}>
                {datetime(w.starts_at)} – {datetime(w.ends_at)}
                {w.reason && ` · ${w.reason}`}
                {w.action !== 'none' && <span className="muted"> · {w.result || ACTIONS[w.action]}</span>}
                {w.status_pages > 0 && <span className="faint"> · on {w.status_pages} status pages</span>}
              </span>
              {canEdit && (
                <button className="btn sm" onClick={() => setEnding(w)}>
                  {w.status === 'active' ? 'End now' : 'Cancel'}
                </button>
              )}
            </div>
          ))}
        </div>
      </Panel>
      {ending && (
        <Confirm
          title={ending.status === 'active' ? 'End maintenance now?' : 'Cancel planned maintenance?'}
          action={ending.status === 'active' ? 'End now' : 'Cancel maintenance'}
          onClose={() => setEnding(null)}
          onConfirm={() =>
            api('POST', `/projects/${project.id}/servers/${s.id}/maintenance/${ending.id}/end`).then(reload)
          }
        >
          {ending.status === 'active'
            ? 'Alerts for this server start again right away.'
            : 'It is removed, along with its entries on status pages.'}
        </Confirm>
      )}
    </div>
  )
}
