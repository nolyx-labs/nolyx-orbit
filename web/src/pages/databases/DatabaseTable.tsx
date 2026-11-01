import { useNavigate } from 'react-router-dom'
import { useProject } from '../../components/layout/context'
import { StatusText } from '../../components/ui/status'
import { ago, bytes, num } from '../../lib/format'
import { Database } from '../../lib/types'
import { dbTone, lag } from './dbHelpers'

export function DBTable({ dbs }: { dbs: Database[] }) {
  const { project } = useProject()
  const nav = useNavigate()
  return (
    <div className="table-wrap" style={{ borderTop: '1px solid var(--border)' }}>
      <table className="t">
        <thead>
          <tr>
            <th>Name</th>
            <th>Status</th>
            <th>Role</th>
            <th className="right">Connections</th>
            <th className="right">TPS</th>
            <th className="right">Cache hit</th>
            <th className="right">Size</th>
            <th className="right">Lag</th>
            <th className="right">Heartbeat</th>
          </tr>
        </thead>
        <tbody>
          {dbs.map((d) => (
            <tr key={d.id} className="link" onClick={() => nav(`/p/${project.id}/databases/${d.id}`)}>
              <td>
                <div className="name">{d.name}</div>
                <div className="sub mono">
                  {d.host}:{d.port}
                  {d.managed && ' · managed'}
                </div>
              </td>
              <td>
                <StatusText tone={dbTone(d)}>{d.status}</StatusText>
              </td>
              <td className="muted">{d.role}</td>
              <td className="right mono">
                {d.stats.connections ?? '—'}
                {d.stats.max_connections ? <span className="faint"> / {d.stats.max_connections}</span> : null}
              </td>
              <td className="right mono">{num(d.stats.tps)}</td>
              <td className="right mono">{d.stats.cache_hit == null ? '—' : `${d.stats.cache_hit.toFixed(1)}%`}</td>
              <td className="right mono">{bytes(d.stats.size)}</td>
              <td className="right mono">{d.role === 'replica' ? lag(d.stats.lag_ms) : '—'}</td>
              <td className="right muted nowrap">{ago(d.last_seen_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
