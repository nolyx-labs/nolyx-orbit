import { Link } from 'react-router-dom'
import { useProject } from '../../components/layout/context'
import { Dot } from '../../components/ui/status'
import { num } from '../../lib/format'
import { Database } from '../../lib/types'
import { clusters, dbTone, lag } from './dbHelpers'

function DBNode({ d }: { d: Database }) {
  const { project } = useProject()
  const isReplica = d.role === 'replica'
  return (
    <Link to={`/p/${project.id}/databases/${d.id}`} className={`topo-node ${dbTone(d)}`}>
      <div className="row" style={{ gap: 6 }}>
        <Dot tone={dbTone(d)} />
        <b>{d.name}</b>
      </div>
      <div className="faint" style={{ fontSize: 11, marginTop: 2 }}>
        {isReplica ? 'REPLICA' : 'PRIMARY'} · {d.server_name}
      </div>
      <div className="mono" style={{ fontSize: 11, marginTop: 4 }}>
        {d.status === 'provisioning'
          ? 'provisioning…'
          : isReplica
            ? `${lag(d.stats.lag_ms)} lag`
            : `${d.stats.connections ?? '—'} conns · ${num(d.stats.tps)} tps`}
      </div>
    </Link>
  )
}

export function Topology({ c }: { c: ReturnType<typeof clusters>[number] }) {
  return (
    <div className="topo">
      {c.primary ? <DBNode d={c.primary} /> : <div className="topo-node">Primary not connected to Orbit</div>}
      {c.extraPrimaries.length > 0 && (
        <div className="callout err" style={{ marginTop: 10, maxWidth: 520 }}>
          Split brain risk: {c.extraPrimaries.map((d) => d.name).join(', ')} also accept writes with the same system
          identifier. Re-sync or remove the old primary.
        </div>
      )}
      {c.replicas.length > 0 && (
        <div className="topo-children">
          {c.replicas.map((r) => (
            <div key={r.id} className="topo-child">
              <DBNode d={r} />
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
