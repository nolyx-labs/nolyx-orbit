import { useState } from 'react'
import { useProject } from '../../../components/layout/context'
import { Dialog } from '../../../components/ui/Dialog'
import { ErrorNote } from '../../../components/ui/states'
import { api } from '../../../lib/api'
import { Channel, METRICS } from './alertMetrics'

export function NewAlert({
  channels,
  onClose,
  onSaved,
}: {
  channels: Channel[]
  onClose: () => void
  onSaved: () => void
}) {
  const { project } = useProject()
  const [f, setF] = useState({ name: '', metric: 'cpu', threshold: '90', duration_minutes: '5', webhook_url: '' })
  const [picked, setPicked] = useState<string[]>([])
  const [error, setError] = useState<string>()
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setF({ ...f, [k]: e.target.value })
  const m = METRICS[f.metric]
  const save = async () => {
    try {
      await api('POST', `/projects/${project.id}/alerts`, {
        ...f,
        name: f.name || `${m.label} alert`,
        threshold: +f.threshold || 0,
        duration_minutes: +f.duration_minutes,
        channel_ids: picked,
      })
      onSaved()
      onClose()
    } catch (e: any) {
      setError(e.message)
    }
  }
  const timeless = f.metric === 'deployment_failed' || f.metric === 'container_restarts'
  return (
    <Dialog
      title="New alert"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" onClick={save}>
            Create alert
          </button>
        </>
      }
    >
      <label className="field">
        <span>Name</span>
        <input className="input" placeholder="High CPU" value={f.name} onChange={set('name')} autoFocus />
      </label>
      <div className="grid-2">
        <label className="field">
          <span>IF</span>
          <select
            className="select"
            value={f.metric}
            onChange={(e) =>
              setF({ ...f, metric: e.target.value, threshold: METRICS[e.target.value].unit === 's' ? '1' : '90' })
            }
          >
            {['server', 'database', 'service', 'container'].map((t) => (
              <optgroup key={t} label={t[0].toUpperCase() + t.slice(1)}>
                {Object.entries(METRICS)
                  .filter(([, v]) => v.target === t)
                  .map(([k, v]) => (
                    <option key={k} value={k}>
                      {v.label}
                    </option>
                  ))}
              </optgroup>
            ))}
          </select>
        </label>
        {m.unit && (
          <label className="field">
            <span>Above ({m.unit === 's' ? 'seconds' : '%'})</span>
            <input className="input mono" value={f.threshold} onChange={set('threshold')} />
          </label>
        )}
      </div>
      <span className="hint faint" style={{ fontSize: 12 }}>
        {m.hint}
      </span>
      {!timeless && (
        <label className="field">
          <span>For (minutes)</span>
          <input
            className="input mono"
            type="number"
            min={1}
            max={1440}
            value={f.duration_minutes}
            onChange={set('duration_minutes')}
          />
        </label>
      )}
      <div className="field">
        <span>THEN notify</span>
        {channels.length ? (
          <div className="checkbox-row">
            {channels.map((c) => (
              <label key={c.id}>
                <input
                  type="checkbox"
                  checked={picked.includes(c.id)}
                  onChange={(e) => setPicked(e.target.checked ? [...picked, c.id] : picked.filter((x) => x !== c.id))}
                />
                {c.name} <span className="faint">({c.type})</span>
              </label>
            ))}
          </div>
        ) : (
          <span className="faint" style={{ fontSize: 12 }}>
            No channels yet: the alert records an event and an incident. Add channels below the rules.
          </span>
        )}
      </div>
      <ErrorNote error={error} />
    </Dialog>
  )
}
