import { useState } from 'react'
import { useCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { Panel } from '../../components/ui/Panel'
import { Segmented } from '../../components/ui/controls'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { StatusText, Tone } from '../../components/ui/status'
import { api } from '../../lib/api'
import { bytes } from '../../lib/format'
import { RefreshInventory, SystemdService, useInsight } from './insight'

const SHOW = ['running', 'failed', 'all'] as const

const tone = (s: SystemdService): Tone => (s.active === 'failed' ? 'err' : s.active === 'active' ? 'ok' : '')

export function ServerSystem({ sid }: { sid: string }) {
  const { project } = useProject()
  const canAct = useCan('member')
  const [ins, loadError, reload] = useInsight(sid)
  const [show, setShow] = useState<(typeof SHOW)[number]>('running')
  const [q, setQ] = useState('')
  const [busy, setBusy] = useState('')
  const [error, setError] = useState<string>()
  const [stopping, setStopping] = useState<string>()

  const act = (unit: string, action: string) =>
    api('POST', `/projects/${project.id}/servers/${sid}/services`, { unit, action }).then(reload)
  const run = (unit: string, action: string) => {
    setBusy(unit)
    setError(undefined)
    act(unit, action)
      .catch((e) => setError(e.message))
      .finally(() => setBusy(''))
  }

  if (loadError) return <Empty title={loadError} />
  if (!ins) return <Loading />
  const inv = ins.inventory
  const services = (inv?.services ?? []).filter(
    (s) =>
      (show === 'all' || (show === 'failed' ? s.active === 'failed' : s.active === 'active')) &&
      (!q || (s.unit + ' ' + s.description).toLowerCase().includes(q.toLowerCase())),
  )
  return (
    <div className="stack">
      <Panel title="System services" actions={<RefreshInventory sid={sid} at={ins.inventory_at} onDone={reload} />}>
        <div className="panel-body row" style={{ flexWrap: 'wrap' }}>
          <Segmented options={SHOW} value={show} onChange={setShow} label="Show" />
          <input
            className="input"
            style={{ width: 240 }}
            placeholder="Filter services"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            aria-label="Filter services"
          />
        </div>
        {inv?.errors?.map((e) => (
          <div key={e} className="callout warn" style={{ margin: '0 14px 12px' }}>
            {e}
          </div>
        ))}
        <ErrorNote error={error} />
        {!inv ? (
          <Empty title="Waiting for the agent's first report" />
        ) : !services.length ? (
          <Empty title="No services match" />
        ) : (
          <div className="table-wrap">
            <table className="t">
              <thead>
                <tr>
                  <th>Service</th>
                  <th>State</th>
                  <th>Starts at boot</th>
                  <th className="right">Memory</th>
                  {canAct && <th className="right">Actions</th>}
                </tr>
              </thead>
              <tbody>
                {services.map((s) => {
                  const on = s.active === 'active'
                  return (
                    <tr key={s.unit}>
                      <td>
                        <div className="name mono">{s.unit}</div>
                        <div className="sub">{s.description}</div>
                      </td>
                      <td>
                        <StatusText tone={tone(s)}>
                          {s.active} <span className="faint">({s.sub})</span>
                        </StatusText>
                      </td>
                      <td className="muted">{s.enabled || '—'}</td>
                      <td className="right mono">{s.memory ? bytes(s.memory) : '—'}</td>
                      {canAct && (
                        <td className="right nowrap">
                          {busy === s.unit ? (
                            <span className="faint">Working…</span>
                          ) : on ? (
                            <span className="row" style={{ gap: 6, justifyContent: 'flex-end' }}>
                              <button className="btn sm" onClick={() => run(s.unit, 'restart')}>
                                Restart
                              </button>
                              <button className="btn sm" onClick={() => run(s.unit, 'reload')}>
                                Reload
                              </button>
                              <button className="btn sm danger" onClick={() => setStopping(s.unit)}>
                                Stop
                              </button>
                            </span>
                          ) : (
                            <button className="btn sm" onClick={() => run(s.unit, 'start')}>
                              Start
                            </button>
                          )}
                        </td>
                      )}
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
      <Panel title="Docker Compose projects">
        {!inv?.compose.length ? (
          <Empty title="No Compose projects found" />
        ) : (
          <div className="list">
            {inv.compose.map((p) => (
              <div key={p.name} className="list-item" style={{ alignItems: 'flex-start' }}>
                <div style={{ width: 220 }}>
                  <div style={{ fontWeight: 500 }}>{p.name}</div>
                  <div className="sub mono faint">{p.working_dir}</div>
                </div>
                <div className="row" style={{ flex: 1, flexWrap: 'wrap', gap: 14 }}>
                  {p.services.map((c) => (
                    <StatusText key={c.container} tone={c.state === 'running' ? 'ok' : 'warn'}>
                      {c.service} <span className="faint">{c.state}</span>
                    </StatusText>
                  ))}
                </div>
              </div>
            ))}
          </div>
        )}
      </Panel>
      {stopping && (
        <Confirm
          title={`Stop ${stopping}?`}
          action="Stop service"
          danger
          onClose={() => setStopping(undefined)}
          onConfirm={() => act(stopping, 'stop')}
        >
          Anything that depends on it stops working until it's started again. It starts again at the next boot if it's
          enabled.
        </Confirm>
      )}
    </div>
  )
}
