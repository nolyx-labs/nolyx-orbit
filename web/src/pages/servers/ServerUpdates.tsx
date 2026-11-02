import { useState } from 'react'
import { useCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { Panel } from '../../components/ui/Panel'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { Badge } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { ago } from '../../lib/format'
import { useLive } from '../../lib/live'
import { Server } from '../../lib/types'
import { PendingPackage, Rollouts } from '../updates/Updates'

interface Check {
  manager: string | null
  packages: PendingPackage[]
  held: string[]
  reboot_required: boolean
  error: string
  checked_at: string | null
}

export function ServerUpdates({ s }: { s: Server }) {
  const { project } = useProject()
  const canEdit = useCan('member')
  const [u, error, reload] = useApi<Check>(`/projects/${project.id}/servers/${s.id}/updates`)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [restart, setRestart] = useState(true)
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState<string>()
  const [actionError, setActionError] = useState<string>()
  const [rebooting, setRebooting] = useState(false)
  useLive(project.id, ['updates'], reload)
  if (error) return <ErrorNote error={error} />
  if (!u) return <Loading />
  const run = (p: Promise<unknown>, done?: string) => {
    setBusy(true)
    setNote(undefined)
    setActionError(undefined)
    p.then(
      () => {
        setNote(done)
        reload()
      },
      (e) => setActionError(e.message),
    ).finally(() => setBusy(false))
  }
  const install = (scope: 'all' | 'security' | 'packages') =>
    run(
      api('POST', `/projects/${project.id}/updates/rollouts`, {
        scope,
        packages: scope === 'packages' ? [...selected].map((name) => ({ name })) : undefined,
        server_ids: [s.id],
        reboot: restart ? 'if_required' : 'never',
      }).then(() => setSelected(new Set())),
      'Installing. Progress shows below.',
    )
  const hold = (name: string, on: boolean) =>
    run(
      api<{ failed: Record<string, string> }>('POST', `/projects/${project.id}/updates/hold`, {
        package: name,
        hold: on,
        server_ids: [s.id],
      }).then((r) => {
        const e = Object.values(r.failed)[0]
        if (e) throw new Error(e)
      }),
    )
  const held = new Set(u.held)
  const rows: PendingPackage[] = [
    ...u.packages,
    ...u.held
      .filter((h) => !u.packages.some((p) => p.name === h))
      .map((name) => ({ name, installed: '', available: '', security: false })),
  ]
  const security = u.packages.some((p) => p.security && !held.has(p.name))
  return (
    <div className="stack">
      {u.error && <div className="callout err">{u.error}</div>}
      {u.reboot_required && (
        <div className="callout row" style={{ gap: 12 }}>
          <span style={{ flex: 1 }}>A restart is needed to finish installing updates.</span>
          {canEdit && (
            <button className="btn sm" onClick={() => setRebooting(true)}>
              Restart now
            </button>
          )}
        </div>
      )}
      <ErrorNote error={actionError} />
      {note && <div className="callout">{note}</div>}
      <Panel
        title={`Waiting updates${u.manager ? ` (${u.manager})` : ''}`}
        actions={
          <>
            <span className="faint" style={{ fontSize: 12 }}>
              checked {ago(u.checked_at)}
            </span>
            {canEdit && (
              <>
                <button
                  className="btn sm"
                  disabled={busy}
                  onClick={() => run(api('POST', `/projects/${project.id}/servers/${s.id}/updates/check`))}
                >
                  Check now
                </button>
                <label className="row faint" style={{ gap: 6, fontSize: 12 }}>
                  <input type="checkbox" checked={restart} onChange={(e) => setRestart(e.target.checked)} />
                  Restart if needed
                </label>
                <button className="btn sm" disabled={busy || !security} onClick={() => install('security')}>
                  Install security only
                </button>
                <button className="btn sm" disabled={busy || !selected.size} onClick={() => install('packages')}>
                  Install {selected.size || ''} selected
                </button>
                <button className="btn sm primary" disabled={busy || !u.packages.length} onClick={() => install('all')}>
                  Install all
                </button>
              </>
            )}
          </>
        }
      >
        {!rows.length ? (
          <Empty title={u.checked_at ? 'Up to date' : 'Not checked yet'}>
            {!u.checked_at && 'Online servers are checked once a day, or use "Check now".'}
          </Empty>
        ) : (
          <div className="table-wrap">
            <table className="t">
              <thead>
                <tr>
                  {canEdit && <th aria-label="Select" />}
                  <th>Package</th>
                  <th>Installed</th>
                  <th>Available</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {rows.map((p) => (
                  <tr key={p.name}>
                    {canEdit && (
                      <td>
                        <input
                          type="checkbox"
                          aria-label={`Select ${p.name}`}
                          disabled={held.has(p.name) || !p.available}
                          checked={selected.has(p.name)}
                          onChange={(e) => {
                            const next = new Set(selected)
                            if (e.target.checked) next.add(p.name)
                            else next.delete(p.name)
                            setSelected(next)
                          }}
                        />
                      </td>
                    )}
                    <td>
                      <span className="row" style={{ gap: 6 }}>
                        <span className="mono">{p.name}</span>
                        {p.security && <Badge tone="err">security</Badge>}
                        {held.has(p.name) && <Badge tone="warn">held</Badge>}
                      </span>
                    </td>
                    <td className="mono muted">{p.installed || '—'}</td>
                    <td className="mono">{p.available || '—'}</td>
                    <td className="right">
                      {canEdit && (
                        <button
                          className="btn sm ghost"
                          disabled={busy}
                          onClick={() => hold(p.name, !held.has(p.name))}
                        >
                          {held.has(p.name) ? 'Let go' : 'Hold'}
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
      <div className="section-title">Installs on this server</div>
      <Rollouts sid={s.id} />
      {rebooting && (
        <Confirm
          title={`Restart ${s.name}?`}
          action="Restart now"
          danger
          onClose={() => setRebooting(false)}
          onConfirm={() => api('POST', `/projects/${project.id}/servers/${s.id}/reboot`)}
        >
          Everything running on it stops until it's back, usually within a few minutes. Its alerts are held for 10
          minutes.
        </Confirm>
      )}
    </div>
  )
}
