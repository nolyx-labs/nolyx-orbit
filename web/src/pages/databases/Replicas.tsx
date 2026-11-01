import { useNavigate } from 'react-router-dom'
import { useProject } from '../../components/layout/context'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { useApi } from '../../lib/api'
import { ago, bytes } from '../../lib/format'
import { useLive } from '../../lib/live'
import { Database } from '../../lib/types'
import { clusters, dbTone, lag, likelyCause } from './dbHelpers'
import { Topology } from './Topology'

export function Replicas() {
  const { project } = useProject()
  const [dbs, , reload] = useApi<Database[]>(`/projects/${project.id}/databases`)
  useLive(project.id, ['databases'], reload)
  const nav = useNavigate()
  if (!dbs)
    return (
      <div className="page">
        <PageHead title="Replicas" />
        <Loading />
      </div>
    )
  const cs = clusters(dbs).filter((c) => c.replicas.length || c.extraPrimaries.length)
  const replicas = dbs.filter((d) => d.role === 'replica')
  return (
    <div className="page">
      <PageHead title="Replicas" sub="Replication topology and health across all clusters" />
      {!cs.length ? (
        <Panel>
          <Empty title="No replicas">Open a primary database and choose Add replica.</Empty>
        </Panel>
      ) : (
        <div className="stack">
          {cs.map((c, i) => (
            <Panel key={i} title={c.primary?.name ?? 'Cluster'}>
              <div className="panel-body">
                <Topology c={c} />
              </div>
            </Panel>
          ))}
          <Panel title="All replicas">
            <table className="t">
              <thead>
                <tr>
                  <th>Replica</th>
                  <th>Health</th>
                  <th>State</th>
                  <th className="right">Lag</th>
                  <th className="right">Replay rate</th>
                  <th>Replay position</th>
                  <th>Server disk</th>
                  <th>Likely cause</th>
                  <th className="right">Heartbeat</th>
                </tr>
              </thead>
              <tbody>
                {replicas.map((r) => (
                  <tr key={r.id} className="link" onClick={() => nav(`/p/${project.id}/databases/${r.id}/replication`)}>
                    <td>
                      <div className="name">{r.name}</div>
                      <div className="sub">{r.server_name}</div>
                    </td>
                    <td>
                      <StatusText tone={dbTone(r)}>{r.status}</StatusText>
                    </td>
                    <td className="muted">{r.stats.receiver?.status ?? '—'}</td>
                    <td className="right mono">{lag(r.stats.lag_ms)}</td>
                    <td className="right mono">
                      {r.stats.replay_rate == null ? '—' : `${bytes(r.stats.replay_rate)}/s`}
                    </td>
                    <td className="mono">{r.stats.wal_position || '—'}</td>
                    <td className="mono">{r.server_usage ? `${r.server_usage.disk.toFixed(0)}%` : '—'}</td>
                    <td className="muted" style={{ fontSize: 12 }}>
                      {r.status !== 'healthy' ? (likelyCause(r) ?? (r.stats.issues ?? []).join('; ')) : '—'}
                    </td>
                    <td className="right muted">{ago(r.last_seen_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Panel>
        </div>
      )}
    </div>
  )
}
