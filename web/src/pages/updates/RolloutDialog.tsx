import { useEffect, useState } from 'react'
import { useProject } from '../../components/layout/context'
import { Dialog } from '../../components/ui/Dialog'
import { ErrorNote } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { api } from '../../lib/api'
import { toLocalInput } from '../status/statusHelpers'
import type { PackageRow } from './Updates'

export interface RolloutPreset {
  scope: 'all' | 'security' | 'packages'
  packages?: string[]
  serverIds?: string[]
}

type Target = { id: string; name: string; status: 'online' | 'offline' }

/** Choose what to install, where, how and when; the server list comes from a dry run of the same request. */
export function RolloutDialog({
  preset,
  packages,
  servers,
  onClose,
  onStarted,
}: {
  preset: RolloutPreset
  packages: PackageRow[]
  servers: { id: string; name: string }[]
  onClose: () => void
  onStarted?: () => void
}) {
  const { project } = useProject()
  const chosen = preset.packages ?? []
  const [scope, setScope] = useState(preset.scope)
  const [versions, setVersions] = useState<Record<string, string>>({})
  const [picked, setPicked] = useState<string[] | null>(preset.serverIds ?? null)
  const [batch, setBatch] = useState(0)
  const [stop, setStop] = useState(true)
  const [reboot, setReboot] = useState<'never' | 'if_required'>('if_required')
  const [later, setLater] = useState('')
  const [targets, setTargets] = useState<Target[]>()
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const body = {
    scope,
    packages:
      scope === 'packages' ? chosen.map((name) => ({ name, version: versions[name]?.trim() || undefined })) : undefined,
    server_ids: picked ?? undefined,
    batch_size: batch,
    stop_on_failure: stop,
    reboot,
    starts_at: later ? new Date(later).toISOString() : undefined,
  }
  const key = JSON.stringify(body)
  useEffect(() => {
    let current = true
    api<{ servers: Target[] }>('POST', `/projects/${project.id}/updates/rollouts`, { ...body, dry_run: true }).then(
      (r) => current && (setTargets(r.servers), setError(undefined)),
      (e) => current && (setTargets(undefined), setError(e.message)),
    )
    return () => {
      current = false
    }
  }, [key])
  const start = () => {
    setBusy(true)
    api('POST', `/projects/${project.id}/updates/rollouts`, body).then(
      () => {
        onStarted?.()
        onClose()
      },
      (e) => {
        setError(e.message)
        setBusy(false)
      },
    )
  }
  const known = (name: string) => {
    const row = packages.find((p) => p.name === name)
    return [...new Set(row?.servers.flatMap((s) => [s.available, s.installed]).filter(Boolean))]
  }
  const offline = targets?.filter((t) => t.status === 'offline').length ?? 0
  const now = new Date()
  return (
    <Dialog
      title="Roll out updates"
      wide
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" disabled={busy || !targets?.length} onClick={start}>
            {later ? 'Schedule' : 'Roll out'} to {targets?.length ?? 0} server{targets?.length === 1 ? '' : 's'}
          </button>
        </>
      }
    >
      <div className="grid-2">
        <label className="field">
          <span>What to install</span>
          <select className="select" value={scope} onChange={(e) => setScope(e.target.value as typeof scope)}>
            {chosen.length > 0 && <option value="packages">The {chosen.length} chosen packages</option>}
            <option value="all">All waiting updates</option>
            <option value="security">Security updates only</option>
          </select>
        </label>
        <label className="field">
          <span>Restart</span>
          <select className="select" value={reboot} onChange={(e) => setReboot(e.target.value as typeof reboot)}>
            <option value="if_required">Restart if an update needs it</option>
            <option value="never">Never restart</option>
          </select>
          <span className="hint">Alerts for a restarting server are held for 10 minutes.</span>
        </label>
      </div>
      {scope === 'packages' && (
        <div className="field">
          <span>Versions</span>
          {chosen.map((name) => (
            <div key={name} className="row" style={{ gap: 8, marginBottom: 6 }}>
              <span className="mono" style={{ flex: 1 }}>
                {name}
              </span>
              <input
                className="input mono"
                style={{ width: 240 }}
                placeholder="latest"
                list={`v-${name}`}
                value={versions[name] ?? ''}
                onChange={(e) => setVersions({ ...versions, [name]: e.target.value })}
                aria-label={`Version of ${name}`}
              />
              <datalist id={`v-${name}`}>
                {known(name).map((v) => (
                  <option key={v} value={v} />
                ))}
              </datalist>
            </div>
          ))}
          <span className="hint">Leave empty for the latest. An exact version can be older than what's installed.</span>
        </div>
      )}
      <div className="field">
        <span>Servers</span>
        <div className="checkbox-row">
          <label>
            <input type="radio" checked={picked === null} onChange={() => setPicked(null)} />
            Every server with these updates waiting
          </label>
          <label>
            <input type="radio" checked={picked !== null} onChange={() => setPicked(preset.serverIds ?? [])} />
            Only the servers I pick
          </label>
        </div>
        {picked !== null && (
          <div className="checkbox-row" style={{ marginTop: 6 }}>
            {servers.map((s) => (
              <label key={s.id}>
                <input
                  type="checkbox"
                  checked={picked.includes(s.id)}
                  onChange={(e) => setPicked(e.target.checked ? [...picked, s.id] : picked.filter((x) => x !== s.id))}
                />
                {s.name}
              </label>
            ))}
          </div>
        )}
      </div>
      <div className="grid-2">
        <label className="field">
          <span>How</span>
          <select
            className="select"
            value={batch ? 'batches' : 'all'}
            onChange={(e) => setBatch(e.target.value === 'all' ? 0 : 1)}
          >
            <option value="all">All servers at once</option>
            <option value="batches">In batches</option>
          </select>
        </label>
        {batch > 0 && (
          <label className="field">
            <span>Servers per batch</span>
            <input
              className="input"
              type="number"
              min={1}
              max={1000}
              value={batch}
              onChange={(e) => setBatch(Math.max(1, Math.min(1000, +e.target.value || 1)))}
            />
          </label>
        )}
      </div>
      <div className="checkbox-row">
        <label>
          <input type="checkbox" checked={stop} onChange={(e) => setStop(e.target.checked)} />
          Stop if a server fails
        </label>
        <label>
          <input
            type="checkbox"
            checked={!!later}
            onChange={(e) => setLater(e.target.checked ? toLocalInput(new Date(now.getTime() + 3600000)) : '')}
          />
          Start later
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
        </label>
      )}
      <div className="field">
        <span>
          Will update {targets?.length ?? '…'} server{targets?.length === 1 ? '' : 's'}
          {offline > 0 && ` (${offline} offline now: skipped if still offline when their turn comes)`}
        </span>
        <div className="checkbox-row">
          {targets?.map((t) => (
            <StatusText key={t.id} tone={t.status === 'online' ? 'ok' : 'err'}>
              {t.name}
            </StatusText>
          ))}
        </div>
      </div>
      <ErrorNote error={error} />
    </Dialog>
  )
}
