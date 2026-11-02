import { useState } from 'react'
import { Link } from 'react-router'
import { useCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Segmented } from '../../components/ui/controls'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { Badge, Dot, StatusText, Tone } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { ago, datetime } from '../../lib/format'
import { useLive } from '../../lib/live'
import { RolloutDialog, RolloutPreset } from './RolloutDialog'

export interface PendingPackage {
  name: string
  installed: string
  available: string
  security: boolean
}

export interface UpdateServer {
  id: string
  name: string
  status: 'online' | 'offline'
  os: string
  manager: string | null
  waiting: number
  security: number
  reboot_required: boolean
  held: string[]
  error: string
  checked_at: string | null
}

export interface PackageRow {
  name: string
  security: boolean
  servers: { id: string; name: string; installed: string; available: string }[]
}

export interface Rollout {
  id: string
  scope: 'all' | 'security' | 'packages'
  packages: { name: string; version?: string }[]
  batch_size: number
  stop_on_failure: boolean
  reboot: 'never' | 'if_required'
  starts_at: string
  status: 'scheduled' | 'running' | 'done' | 'failed' | 'cancelled'
  created_at: string
  created_by: string | null
  servers: {
    server_id: string
    name: string
    status: 'waiting' | 'running' | 'done' | 'failed' | 'skipped'
    result: string
    operation_id: string | null
  }[]
}

const VIEWS = ['By package', 'By server', 'Rollouts'] as const

const rolloutTone: Record<string, Tone> = {
  scheduled: 'info',
  running: 'info',
  waiting: '',
  done: 'ok',
  failed: 'err',
  cancelled: 'warn',
  skipped: 'warn',
}

const rolloutLabel: Record<string, string> = {
  scheduled: 'Scheduled',
  running: 'In progress',
  waiting: 'Waiting',
  done: 'Done',
  failed: 'Failed',
  cancelled: 'Cancelled',
  skipped: 'Skipped',
}

export function Updates() {
  const { project } = useProject()
  const canEdit = useCan('member')
  const [view, setView] = useState<(typeof VIEWS)[number]>('By package')
  const [data, error, reload] = useApi<{ servers: UpdateServer[]; packages: PackageRow[] }>(
    `/projects/${project.id}/updates`,
  )
  const [rolling, setRolling] = useState<RolloutPreset | null>(null)
  const [note, setNote] = useState<string>()
  useLive(project.id, ['updates', 'servers'], reload)
  if (error)
    return (
      <div className="page">
        <ErrorNote error={error} />
      </div>
    )
  if (!data) return <Loading />
  const lastCheck = data.servers.reduce<string | null>(
    (m, s) => (s.checked_at && (!m || s.checked_at > m) ? s.checked_at : m),
    null,
  )
  const security = data.packages.filter((p) => p.security).length
  const restart = data.servers.filter((s) => s.reboot_required).length
  const checkAll = () =>
    api<{ servers: number }>('POST', `/projects/${project.id}/updates/check`).then(
      (r) =>
        setNote(`Checking ${r.servers} online server${r.servers === 1 ? '' : 's'}. Results appear as they finish.`),
      (e) => setNote(e.message),
    )
  return (
    <div className="page">
      <PageHead title="Updates" sub="Operating system package updates on your servers, checked once a day">
        {canEdit && (
          <>
            <button className="btn" onClick={checkAll}>
              Check all now
            </button>
            <button className="btn primary" onClick={() => setRolling({ scope: 'all' })}>
              Roll out
            </button>
          </>
        )}
      </PageHead>
      {note && (
        <div className="callout" style={{ marginBottom: 16 }}>
          {note}
        </div>
      )}
      <div className="cards" style={{ marginBottom: 16 }}>
        <div className="card">
          <div className="k">Waiting updates</div>
          <div className="v">
            {data.packages.length}
            <small>packages</small>
          </div>
          <div className="s">
            <Dot tone={data.packages.length ? 'warn' : 'ok'} />
            on {data.servers.filter((s) => s.waiting).length} servers
          </div>
        </div>
        <div className="card">
          <div className="k">Security updates</div>
          <div className="v">
            {security}
            <small>packages</small>
          </div>
          <div className="s">
            <Dot tone={security ? 'err' : 'ok'} />
            {security ? 'Install these soon' : 'None waiting'}
          </div>
        </div>
        <div className="card">
          <div className="k">Need a restart</div>
          <div className="v">
            {restart}
            <small>servers</small>
          </div>
          <div className="s">
            <Dot tone={restart ? 'warn' : 'ok'} />
            {restart ? 'To finish installed updates' : 'None'}
          </div>
        </div>
        <div className="card">
          <div className="k">Last check</div>
          <div className="v">{ago(lastCheck)}</div>
          <div className="s">{data.servers.filter((s) => s.error).length} servers couldn't be checked</div>
        </div>
      </div>
      <div className="section-title">
        <Segmented options={VIEWS} value={view} onChange={setView} label="View" />
      </div>
      {view === 'By package' && <ByPackage data={data} onRollOut={setRolling} />}
      {view === 'By server' && <ByServer servers={data.servers} />}
      {view === 'Rollouts' && <Rollouts />}
      {rolling && (
        <RolloutDialog
          preset={rolling}
          packages={data.packages}
          servers={data.servers}
          onClose={() => setRolling(null)}
          onStarted={() => setView('Rollouts')}
        />
      )}
    </div>
  )
}

function ByPackage({
  data,
  onRollOut,
}: {
  data: { servers: UpdateServer[]; packages: PackageRow[] }
  onRollOut: (p: RolloutPreset) => void
}) {
  const { project } = useProject()
  const canEdit = useCan('member')
  const [q, setQ] = useState('')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [letGo, setLetGo] = useState<{ name: string; servers: { id: string; name: string }[] } | null>(null)
  const heldOn = new Map<string, { id: string; name: string }[]>()
  for (const s of data.servers) for (const h of s.held) heldOn.set(h, [...(heldOn.get(h) ?? []), s])
  // Held packages without an update waiting still get a row, so they can be let go.
  const rows: PackageRow[] = [
    ...data.packages,
    ...[...heldOn.keys()]
      .filter((n) => !data.packages.some((p) => p.name === n))
      .map((name) => ({ name, security: false, servers: [] })),
  ].filter((p) => p.name.includes(q.trim()))
  const versions = (xs: string[]) => [...new Set(xs.filter(Boolean))].join(', ') || '—'
  const toggle = (name: string) => {
    const next = new Set(selected)
    if (next.has(name)) next.delete(name)
    else next.add(name)
    setSelected(next)
  }
  return (
    <Panel
      title={
        <input
          className="input"
          style={{ width: 240 }}
          placeholder="Search packages"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          aria-label="Search packages"
        />
      }
      actions={
        canEdit && (
          <button
            className="btn sm primary"
            disabled={!selected.size}
            onClick={() => onRollOut({ scope: 'packages', packages: [...selected] })}
          >
            Roll out {selected.size || ''} selected
          </button>
        )
      }
    >
      {!rows.length ? (
        <Empty title={q ? 'No matching packages' : 'Everything is up to date'}>
          {!q && 'Servers are checked once a day. Use "Check all now" to check right away.'}
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
                <th className="right">Servers</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {rows.map((p) => {
                const held = heldOn.get(p.name)
                return (
                  <tr key={p.name}>
                    {canEdit && (
                      <td>
                        <input
                          type="checkbox"
                          aria-label={`Select ${p.name}`}
                          disabled={!p.servers.length}
                          checked={selected.has(p.name)}
                          onChange={() => toggle(p.name)}
                        />
                      </td>
                    )}
                    <td>
                      <span className="row" style={{ gap: 6 }}>
                        <span className="mono">{p.name}</span>
                        {p.security && <Badge tone="err">security</Badge>}
                        {held && <Badge tone="warn">held on {held.length}</Badge>}
                      </span>
                    </td>
                    <td className="mono muted">{versions(p.servers.map((s) => s.installed))}</td>
                    <td className="mono">{versions(p.servers.map((s) => s.available))}</td>
                    <td className="right" title={p.servers.map((s) => s.name).join(', ')}>
                      {p.servers.length}
                    </td>
                    <td className="right">
                      {canEdit && held && (
                        <button className="btn sm" onClick={() => setLetGo({ name: p.name, servers: held })}>
                          Let go
                        </button>
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
      {letGo && (
        <Confirm
          title={`Let go of ${letGo.name}?`}
          action="Let go"
          onClose={() => setLetGo(null)}
          onConfirm={() =>
            api<{ failed: Record<string, string> }>('POST', `/projects/${project.id}/updates/hold`, {
              package: letGo.name,
              hold: false,
              server_ids: letGo.servers.map((s) => s.id),
            }).then((r) => {
              const failed = Object.entries(r.failed)
              if (failed.length) throw new Error(failed.map(([s, e]) => `${s}: ${e}`).join('; '))
            })
          }
        >
          It's held at its current version on {letGo.servers.map((s) => s.name).join(', ')}. Once let go, it's updated
          along with everything else.
        </Confirm>
      )}
    </Panel>
  )
}

function ByServer({ servers }: { servers: UpdateServer[] }) {
  const { project } = useProject()
  return (
    <Panel>
      {!servers.length ? (
        <Empty title="No servers yet" />
      ) : (
        <div className="table-wrap">
          <table className="t">
            <thead>
              <tr>
                <th>Server</th>
                <th className="right">Waiting</th>
                <th className="right">Security</th>
                <th>Restart</th>
                <th>Held</th>
                <th className="right">Checked</th>
              </tr>
            </thead>
            <tbody>
              {servers.map((s) => (
                <tr key={s.id}>
                  <td>
                    <Link className="name" to={`/p/${project.id}/servers/${s.id}/updates`}>
                      {s.name}
                    </Link>
                    <div className="sub">
                      {s.error ? <span className="text-err">{s.error}</span> : s.os}
                      {s.status === 'offline' && ' · offline'}
                    </div>
                  </td>
                  <td className="right mono">{s.checked_at ? s.waiting : '—'}</td>
                  <td className={`right mono ${s.security ? 'text-err' : ''}`}>{s.checked_at ? s.security : '—'}</td>
                  <td>{s.reboot_required ? <Badge tone="warn">needed</Badge> : <span className="faint">no</span>}</td>
                  <td className="mono muted">{s.held.join(', ') || '—'}</td>
                  <td className="right muted">{ago(s.checked_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  )
}

export function rolloutTitle(r: Rollout) {
  if (r.scope === 'all') return 'All waiting updates'
  if (r.scope === 'security') return 'Security updates'
  return r.packages.map((p) => (p.version ? `${p.name} ${p.version}` : p.name)).join(', ')
}

/** Rollouts with per-server progress; filtered to one server when sid is given. */
export function Rollouts({ sid }: { sid?: string }) {
  const { project } = useProject()
  const canEdit = useCan('member')
  const [rollouts, , reload] = useApi<Rollout[]>(
    `/projects/${project.id}/updates/rollouts${sid ? `?server=${sid}` : ''}`,
  )
  const [cancelling, setCancelling] = useState<Rollout | null>(null)
  useLive(project.id, ['updates'], reload)
  if (!rollouts) return <Loading />
  if (!rollouts.length)
    return (
      <Panel>
        <Empty title="No rollouts yet">Roll out updates to install them on your servers.</Empty>
      </Panel>
    )
  return (
    <div className="stack">
      {rollouts.map((r) => {
        const finished = r.servers.filter((s) => !['waiting', 'running'].includes(s.status)).length
        return (
          <Panel
            key={r.id}
            title={
              <span className="row" style={{ gap: 8 }}>
                <span>{rolloutTitle(r)}</span>
                <StatusText tone={rolloutTone[r.status]}>{rolloutLabel[r.status]}</StatusText>
              </span>
            }
            actions={
              <>
                <span className="faint" style={{ fontSize: 12 }}>
                  {r.status === 'scheduled'
                    ? `starts ${datetime(r.starts_at)}`
                    : `${finished} of ${r.servers.length} finished`}
                  {' · '}
                  {r.batch_size ? `batches of ${r.batch_size}` : 'all at once'}
                  {r.reboot === 'if_required' && ' · restarts if needed'}
                  {r.created_by && ` · ${r.created_by}`}
                </span>
                {canEdit && (r.status === 'scheduled' || r.status === 'running') && (
                  <button className="btn sm" onClick={() => setCancelling(r)}>
                    Cancel
                  </button>
                )}
              </>
            }
          >
            <div className="list">
              {r.servers.map((s) => (
                <div key={s.server_id} className="list-item">
                  <span style={{ width: 160 }} className="nowrap">
                    {s.name}
                  </span>
                  <span style={{ width: 120 }}>
                    <StatusText tone={rolloutTone[s.status]}>{rolloutLabel[s.status]}</StatusText>
                  </span>
                  <span className="muted" style={{ flex: 1, minWidth: 0 }}>
                    {s.result}
                  </span>
                  {s.operation_id && (
                    <Link className="btn sm ghost" to={`/p/${project.id}/operations/${s.operation_id}`}>
                      Log
                    </Link>
                  )}
                </div>
              ))}
            </div>
          </Panel>
        )
      })}
      {cancelling && (
        <Confirm
          title="Cancel this rollout?"
          action="Cancel rollout"
          danger
          onClose={() => setCancelling(null)}
          onConfirm={() => api('POST', `/projects/${project.id}/updates/rollouts/${cancelling.id}/cancel`).then(reload)}
        >
          Servers that haven't started are skipped. Servers already installing finish what they're doing.
        </Confirm>
      )}
    </div>
  )
}
