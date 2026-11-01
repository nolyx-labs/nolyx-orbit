import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useCan, useProject } from '../../../components/layout/context'
import { Confirm } from '../../../components/ui/Confirm'
import { Panel } from '../../../components/ui/Panel'
import { Empty } from '../../../components/ui/states'
import { StatusText } from '../../../components/ui/status'
import { api, useApi } from '../../../lib/api'
import { ago, bytes } from '../../../lib/format'
import { opPath } from '../../../lib/paths'
import { Database } from '../../../lib/types'
import { clusters, lag } from '../dbHelpers'
import { ReplicaDialog } from '../ReplicaDialog'
import { Topology } from '../Topology'

export function DBReplication({ d }: { d: Database }) {
  const { project } = useProject()
  const canAdmin = useCan('admin')
  const nav = useNavigate()
  const [dbs] = useApi<Database[]>(`/projects/${project.id}/databases`)
  const [adding, setAdding] = useState(false)
  const [confirm, setConfirm] = useState<'promote' | 'resync' | null>(null)
  const c =
    dbs && clusters(dbs).find((x) => [x.primary, ...x.replicas, ...x.extraPrimaries].some((m) => m?.id === d.id))
  const st = d.stats
  return (
    <div className="stack">
      {c && (
        <Panel title="Topology">
          <div className="panel-body">
            <Topology c={c} />
          </div>
        </Panel>
      )}
      {d.role === 'replica' ? (
        <Panel
          title="Replica status"
          actions={
            canAdmin && (
              <>
                {d.managed && d.primary_id && (
                  <button className="btn sm" onClick={() => setConfirm('resync')}>
                    Re-sync
                  </button>
                )}
                <button className="btn sm danger" onClick={() => setConfirm('promote')}>
                  Promote to primary
                </button>
              </>
            )
          }
        >
          <div className="panel-body kv">
            <div className="k">WAL receiver</div>
            <div>
              <StatusText tone={st.receiver?.status === 'streaming' ? 'ok' : 'err'}>
                {st.receiver?.status ?? 'not running'}
              </StatusText>
            </div>
            <div className="k">Upstream</div>
            <div className="mono">{st.receiver ? `${st.receiver.sender_host}:${st.receiver.sender_port}` : '—'}</div>
            <div className="k">Replication lag</div>
            <div className="mono">{lag(st.lag_ms)}</div>
            <div className="k">Replay rate</div>
            <div className="mono">{st.replay_rate == null ? '—' : `${bytes(st.replay_rate)}/s`}</div>
            <div className="k">Replay position</div>
            <div className="mono">{st.wal_position || '—'}</div>
            <div className="k">Last heartbeat</div>
            <div>{ago(d.last_seen_at)}</div>
            <div className="k">Server disk</div>
            <div className="mono">
              {d.server_usage
                ? `${d.server_usage.disk.toFixed(0)}% used · ${d.server_usage.io_util.toFixed(0)}% I/O busy`
                : '—'}
            </div>
          </div>
        </Panel>
      ) : (
        <Panel
          title="Connected replicas"
          actions={
            canAdmin && (
              <button
                className="btn sm primary"
                onClick={() => setAdding(true)}
                disabled={!d.replication_ready}
                title={d.replication_ready ? '' : 'Replication credentials are needed: see the database settings'}
              >
                Add replica
              </button>
            )
          }
        >
          {!st.replicas?.length ? (
            <Empty title="No streaming replicas">
              {!d.replication_ready &&
                'Orbit-managed primaries support one-click replicas. For existing primaries, set a replication password when connecting.'}
            </Empty>
          ) : (
            <table className="t">
              <thead>
                <tr>
                  <th>Application name</th>
                  <th>Client</th>
                  <th>State</th>
                  <th>Sync</th>
                  <th>Replay LSN</th>
                  <th className="right">Lag bytes</th>
                  <th className="right">Replay lag</th>
                </tr>
              </thead>
              <tbody>
                {st.replicas.map((r, i) => (
                  <tr key={i}>
                    <td className="mono">{r.name}</td>
                    <td className="mono muted">{r.client_addr}</td>
                    <td>
                      <StatusText tone={r.state === 'streaming' ? 'ok' : 'warn'}>{r.state}</StatusText>
                    </td>
                    <td className="muted">{r.sync_state}</td>
                    <td className="mono">{r.replay_lsn}</td>
                    <td className="right mono">{bytes(r.lag_bytes)}</td>
                    <td className="right mono">{lag(r.replay_lag_ms)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Panel>
      )}
      {adding && <ReplicaDialog primary={d} onClose={() => setAdding(false)} />}
      {confirm === 'promote' && (
        <Confirm
          title={`Promote ${d.name} to primary?`}
          action="Promote"
          danger
          typeToConfirm={d.name}
          onClose={() => setConfirm(null)}
          onConfirm={() =>
            api<{ operation_id: string }>('POST', `/projects/${project.id}/databases/${d.id}/promote`, {
              confirm: d.name,
            }).then((r) => nav(opPath(project.id, r.operation_id)))
          }
        >
          <div className="callout err" style={{ marginBottom: 8 }}>
            This is irreversible. The replica stops following its primary and starts accepting writes.
          </div>
          Orbit does not stop the old primary or move your applications. Until you do, both databases accept writes and
          will diverge (split brain). Point applications at <span className="mono">{d.name}</span>, then re-sync or
          remove the old primary.
        </Confirm>
      )}
      {confirm === 'resync' && (
        <Confirm
          title={`Re-sync ${d.name}?`}
          action="Re-sync"
          danger
          typeToConfirm={d.name}
          onClose={() => setConfirm(null)}
          onConfirm={() =>
            api<{ operation_id: string }>('POST', `/projects/${project.id}/databases/${d.id}/resync`, {
              confirm: d.name,
            }).then((r) => nav(opPath(project.id, r.operation_id)))
          }
        >
          Deletes this replica's data volume and clones the primary again with pg_basebackup. The replica is unavailable
          until the copy completes.
        </Confirm>
      )}
    </div>
  )
}
