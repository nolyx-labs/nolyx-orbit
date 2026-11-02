import { useState } from 'react'
import { useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { Dialog } from '../../components/ui/Dialog'
import { ErrorNote, Loading } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { ago } from '../../lib/format'

interface Destination {
  configured: boolean
  enabled?: boolean
  last_at?: string | null
  last_error?: string
  behind?: number
}

/** Copy this project's logs into a PostgreSQL database the customer owns. */
export function LogDestination({ onClose }: { onClose: () => void }) {
  const { project } = useProject()
  const path = `/projects/${project.id}/logs/destination`
  const [d, , reload] = useApi<Destination>(path)
  const [url, setUrl] = useState('')
  const [enabled, setEnabled] = useState<boolean>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [removing, setRemoving] = useState(false)
  const on = enabled ?? d?.enabled ?? true
  const save = () => {
    setBusy(true)
    setError(undefined)
    api('PUT', path, { url, enabled: on })
      .then(
        () => {
          setUrl('')
          reload()
        },
        (e) => setError(e.message),
      )
      .finally(() => setBusy(false))
  }
  return (
    <Dialog
      title="Copy logs to your database"
      onClose={onClose}
      footer={
        <>
          {d?.configured && (
            <button className="btn danger" style={{ marginRight: 'auto' }} onClick={() => setRemoving(true)}>
              Stop and remove
            </button>
          )}
          <button className="btn" onClick={onClose}>
            Close
          </button>
          <button className="btn primary" onClick={save} disabled={busy || (!url && !d?.configured)}>
            {busy ? 'Connecting…' : 'Save'}
          </button>
        </>
      }
    >
      <div className="muted">
        Every 30 seconds Orbit copies new log lines from this project into an <span className="mono">orbit_logs</span>{' '}
        table in a PostgreSQL database you own, so you can keep them as long as you like and query them yourself. Orbit
        creates the table. The user only needs permission to create tables in the database and insert into them.
      </div>
      {!d ? (
        <Loading />
      ) : (
        d.configured && (
          <div className="kv">
            <div className="k">Status</div>
            <div>
              {d.last_error ? (
                <StatusText tone="err">{d.last_error}</StatusText>
              ) : !d.enabled ? (
                <StatusText tone="">Paused</StatusText>
              ) : (
                <StatusText tone="ok">Copying{d.last_at ? `, last copy ${ago(d.last_at)}` : ''}</StatusText>
              )}
            </div>
            <div className="k">Waiting to copy</div>
            <div className="mono">{d.behind?.toLocaleString()} lines</div>
          </div>
        )
      )}
      <label className="field">
        <span>{d?.configured ? 'New database URL (leave blank to keep the saved one)' : 'Database URL'}</span>
        <input
          className="input mono"
          type="password"
          autoComplete="off"
          placeholder="postgres://user:password@db.example.com:5432/logs?sslmode=require"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
        />
      </label>
      <label className="row">
        <input type="checkbox" checked={on} onChange={(e) => setEnabled(e.target.checked)} /> Copy logs
      </label>
      <div className="faint">
        Orbit keeps lines for 7 days, so if your database is unreachable for longer than that, the oldest lines are
        lost. Private and local addresses can't be used.
      </div>
      <ErrorNote error={error} />
      {removing && (
        <Confirm
          title="Stop copying logs?"
          action="Stop and remove"
          danger
          onClose={() => setRemoving(false)}
          onConfirm={() => api('DELETE', path).then(onClose)}
        >
          Orbit forgets the database URL. Lines already copied stay in your database.
        </Confirm>
      )}
    </Dialog>
  )
}
