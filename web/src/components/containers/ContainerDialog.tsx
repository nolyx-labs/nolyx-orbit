import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, useApi } from '../../lib/api'
import { bytes } from '../../lib/format'
import { Container, Series } from '../../lib/types'
import { Chart } from '../charts/Chart'
import { useCan, useProject } from '../layout/context'
import { Confirm } from '../ui/Confirm'
import { RangePicker } from '../ui/controls'
import { Dialog } from '../ui/Dialog'
import { ErrorNote } from '../ui/states'
import { Badge, StatusText, containerTone } from '../ui/status'

type Act = 'start' | 'stop' | 'restart' | 'remove'

export function ContainerDialog({
  c,
  onClose,
  onChanged,
}: {
  c: Container
  onClose: () => void
  onChanged: () => void
}) {
  const { project } = useProject()
  const canOperate = useCan('member')
  const canRemove = useCan('admin')
  const base = `/projects/${project.id}/servers/${c.server_id}/containers/${c.id}`
  const [tab, setTab] = useState<'logs' | 'metrics' | 'details'>('logs')
  const [range, setRange] = useState('1h')
  const [tail, setTail] = useState(200)
  const [logs, logsErr, reloadLogs] = useApi<{ logs: string }>(tab === 'logs' ? `${base}/logs?tail=${tail}` : null)
  const [metrics] = useApi<Series>(tab === 'metrics' ? `${base}/metrics?range=${range}` : null)
  const [confirm, setConfirm] = useState<Act | null>(null)
  const [filter, setFilter] = useState('')
  const running = c.state === 'running'

  const run = (a: Act) =>
    api('POST', `${base}/${a}`, a === 'remove' ? { confirm: c.name } : undefined).then(() => {
      onChanged()
      if (a === 'remove') onClose()
    })

  const lines = (logs?.logs ?? '')
    .split('\n')
    .filter((l) => l && (!filter || l.toLowerCase().includes(filter.toLowerCase())))

  return (
    <Dialog title={c.name} onClose={onClose} wide>
      <div className="row" style={{ flexWrap: 'wrap', marginTop: -6 }}>
        <StatusText tone={containerTone(c)}>
          {c.state}
          {c.health && ` · ${c.health}`}
        </StatusText>
        <span className="faint">·</span>
        <span className="muted">{c.server_name}</span>
        {c.labels['orbit.app'] && <Badge>app: {c.labels['orbit.app']}</Badge>}
        <div className="spacer" />
        {canOperate && (
          <>
            {running ? (
              <>
                <button className="btn sm" onClick={() => setConfirm('restart')}>
                  Restart
                </button>
                <button className="btn sm" onClick={() => setConfirm('stop')}>
                  Stop
                </button>
              </>
            ) : (
              <button className="btn sm" onClick={() => setConfirm('start')}>
                Start
              </button>
            )}
            {canRemove && (
              <button className="btn sm danger" onClick={() => setConfirm('remove')}>
                Remove
              </button>
            )}
          </>
        )}
      </div>
      <div className="tabs" style={{ marginBottom: 4 }}>
        {(['logs', 'metrics', 'details'] as const).map((t) => (
          <a
            key={t}
            href="#"
            className={tab === t ? 'active' : ''}
            onClick={(e) => {
              e.preventDefault()
              setTab(t)
            }}
          >
            {t[0].toUpperCase() + t.slice(1)}
          </a>
        ))}
      </div>
      {tab === 'logs' && (
        <div className="stack" style={{ gap: 8 }}>
          <div className="row">
            <input
              className="input"
              placeholder="Filter lines…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              style={{ maxWidth: 260 }}
            />
            <select
              className="select"
              style={{ width: 120 }}
              value={tail}
              onChange={(e) => setTail(+e.target.value)}
              aria-label="Lines"
            >
              {[100, 200, 500, 1000, 5000].map((n) => (
                <option key={n} value={n}>
                  last {n}
                </option>
              ))}
            </select>
            <div className="spacer" />
            <button className="btn sm" onClick={reloadLogs}>
              Refresh
            </button>
          </div>
          <ErrorNote error={logsErr} />
          <pre className="code logs">
            {!logs && !logsErr && 'Fetching from agent…'}
            {logs && !lines.length && 'No log lines.'}
            {lines.map((l, i) => {
              const sp = l.indexOf(' ')
              const ts = l.slice(0, sp)
              const rest = l.slice(sp + 1)
              const lvl = /\b(error|fatal|panic)\b/i.test(rest)
                ? 'lvl-err'
                : /\bwarn(ing)?\b/i.test(rest)
                  ? 'lvl-warn'
                  : ''
              return (
                <div key={i}>
                  <span className="ts">{ts.slice(11, 19)}</span> <span className={lvl}>{rest}</span>
                </div>
              )
            })}
          </pre>
        </div>
      )}
      {tab === 'metrics' && (
        <div className="stack" style={{ gap: 8 }}>
          <div className="row">
            <div className="spacer" />
            <RangePicker value={range} onChange={setRange} />
          </div>
          {metrics ? (
            <>
              <Chart
                title="CPU (% of one core)"
                t={metrics.t}
                series={[{ label: 'CPU', values: metrics.cpu, color: 'series-1' }]}
                format={(v) => `${v.toFixed(1)}%`}
                height={130}
              />
              <Chart
                title="Memory"
                t={metrics.t}
                series={[{ label: 'Memory', values: metrics.mem, color: 'series-1' }]}
                format={(v) => bytes(v)}
                height={130}
              />
            </>
          ) : (
            <div className="chart-empty">Loading…</div>
          )}
        </div>
      )}
      {tab === 'details' && (
        <div className="kv">
          <div className="k">Container ID</div>
          <div className="mono">{c.id.slice(0, 24)}…</div>
          <div className="k">Image</div>
          <div className="mono">{c.image}</div>
          <div className="k">Created</div>
          <div>{new Date(c.created).toLocaleString()}</div>
          <div className="k">Ports</div>
          <div className="mono">{c.ports.join(', ') || '—'}</div>
          <div className="k">Memory limit</div>
          <div className="mono">{c.mem_limit ? bytes(c.mem_limit) : '—'}</div>
          <div className="k">Restarts</div>
          <div className="mono">{c.restart_count}</div>
          <div className="k">Labels</div>
          <div className="mono" style={{ fontSize: 11 }}>
            {Object.entries(c.labels)
              .slice(0, 12)
              .map(([k, v]) => (
                <div key={k}>
                  {k}={v}
                </div>
              ))}
            {Object.keys(c.labels).length === 0 && '—'}
          </div>
          <div className="k">Server</div>
          <div>
            <Link to={`/p/${project.id}/servers/${c.server_id}/overview`} onClick={onClose}>
              {c.server_name} →
            </Link>
          </div>
        </div>
      )}
      {confirm && (
        <Confirm
          title={`${confirm[0].toUpperCase() + confirm.slice(1)} ${c.name}?`}
          action={confirm[0].toUpperCase() + confirm.slice(1)}
          danger={confirm !== 'start'}
          typeToConfirm={confirm === 'remove' ? c.name : undefined}
          onConfirm={() => run(confirm)}
          onClose={() => setConfirm(null)}
        >
          {confirm === 'remove' && (
            <>
              This force-removes the container. Its writable layer is deleted; named volumes are kept. This cannot be
              undone.
            </>
          )}
          {confirm === 'stop' && (
            <>The container will receive SIGTERM and be killed after 10 seconds. Traffic to it stops.</>
          )}
          {confirm === 'restart' && <>The container will be stopped and started again. Expect a brief interruption.</>}
          {confirm === 'start' && <>Start the container on {c.server_name}.</>}
          <div className="faint" style={{ marginTop: 8 }}>
            This action is recorded in the audit log.
          </div>
        </Confirm>
      )}
    </Dialog>
  )
}
