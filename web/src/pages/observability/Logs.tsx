import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router'
import { useProject } from '../../components/layout/context'
import { RANGES, Segmented } from '../../components/ui/controls'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { useApi } from '../../lib/api'
import { useLive } from '../../lib/live'
import { Server } from '../../lib/types'

interface Line {
  id: number
  ts: string
  level: string
  stream: string
  line: string
  container: string
  app: string
  server_id: string
  server_name: string
}

const LEVELS = ['error', 'warn', 'info', 'debug']

export function Logs() {
  const { project } = useProject()
  const [params, setParams] = useSearchParams()
  const [q, setQ] = useState(params.get('q') ?? '')
  const [debounced, setDebounced] = useState(q)
  const [range, setRange] = useState<(typeof RANGES)[number]>('1h')
  const [levels, setLevels] = useState<string[]>([])
  const [live, setLive] = useState(true)
  const server = params.get('server') ?? ''
  const app = params.get('app') ?? ''
  const container = params.get('container') ?? ''
  const [servers] = useApi<Server[]>(`/projects/${project.id}/servers`)
  const [apps] = useApi<{ name: string }[]>(`/projects/${project.id}/apps`)
  useEffect(() => {
    const t = setTimeout(() => setDebounced(q), 250)
    return () => clearTimeout(t)
  }, [q])
  const qs = new URLSearchParams({ q: debounced, range, level: levels.join(','), server, app, container })
  const [lines, error, reload] = useApi<Line[]>(`/projects/${project.id}/logs?${qs}`)
  useLive(project.id, live ? ['logs'] : [], reload, 3000)
  const setParam = (k: string, v: string) => {
    const p = new URLSearchParams(params)
    v ? p.set(k, v) : p.delete(k)
    setParams(p, { replace: true })
  }
  return (
    <div className="page">
      <PageHead title="Logs" sub="Container logs from every server, shipped by the agent and kept for 7 days">
        <label className="row muted" style={{ fontSize: 12 }}>
          <input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} /> Live
        </label>
      </PageHead>
      <div className="row" style={{ marginBottom: 10, flexWrap: 'wrap' }}>
        <input
          className="input mono"
          style={{ maxWidth: 320 }}
          placeholder="Search text…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          autoFocus
        />
        <select
          className="select"
          style={{ width: 170 }}
          value={server}
          onChange={(e) => setParam('server', e.target.value)}
          aria-label="Server"
        >
          <option value="">All servers</option>
          {servers?.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
        <select
          className="select"
          style={{ width: 170 }}
          value={app}
          onChange={(e) => setParam('app', e.target.value)}
          aria-label="Service"
        >
          <option value="">All services</option>
          {apps?.map((a) => (
            <option key={a.name} value={a.name}>
              {a.name}
            </option>
          ))}
        </select>
        {container && (
          <button className="btn sm" onClick={() => setParam('container', '')}>
            container: {container} ×
          </button>
        )}
        <div className="checkbox-row">
          {LEVELS.map((l) => (
            <label key={l}>
              <input
                type="checkbox"
                checked={levels.includes(l)}
                onChange={(e) => setLevels(e.target.checked ? [...levels, l] : levels.filter((x) => x !== l))}
              />
              <span className={`lvl ${l}`}>{l}</span>
            </label>
          ))}
        </div>
        <div className="spacer" />
        <Segmented label="Time range" options={RANGES} value={range} onChange={setRange} />
      </div>
      <Panel>
        {error ? (
          <Empty title={error} />
        ) : !lines ? (
          <Loading />
        ) : !lines.length ? (
          <Empty title="No matching log lines">
            Logs from running containers arrive within a few seconds. Database containers managed by Orbit are included.
          </Empty>
        ) : (
          <div className="logview" style={{ padding: '6px 0', maxHeight: 'calc(100vh - 230px)', overflow: 'auto' }}>
            {lines.map((l) => (
              <div key={l.id} className="ln">
                <span className="ts" title={l.ts}>
                  {new Date(l.ts).toLocaleString([], {
                    month: 'short',
                    day: '2-digit',
                    hour: '2-digit',
                    minute: '2-digit',
                    second: '2-digit',
                    hour12: false,
                  })}
                </span>
                <span className={`lvl ${l.level}`}>{l.level}</span>
                <span className="src" title={`${l.server_name} / ${l.container}`}>
                  <a
                    href="#"
                    onClick={(e) => {
                      e.preventDefault()
                      setParam('container', l.container)
                    }}
                  >
                    {l.app || l.container}
                  </a>
                  <span className="faint"> · {l.server_name}</span>
                </span>
                <span className="msg">{l.line}</span>
              </div>
            ))}
            {lines.length === 300 && (
              <div className="faint" style={{ padding: '8px 14px' }}>
                Showing the newest 300 lines. Narrow the search to see more.
              </div>
            )}
          </div>
        )}
      </Panel>
    </div>
  )
}
