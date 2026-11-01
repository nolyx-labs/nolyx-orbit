import { Tone } from '../../components/ui/status'
import { Database } from '../../lib/types'

export const dbTone = (d: Pick<Database, 'status'>): Tone =>
  d.status === 'healthy'
    ? 'ok'
    : d.status === 'degraded' || d.status === 'provisioning'
      ? 'warn'
      : d.status === 'unknown'
        ? ''
        : 'err'

export const lag = (ms: number | null | undefined) =>
  ms == null ? '—' : ms < 1000 ? `${Math.round(ms)}ms` : `${(ms / 1000).toFixed(1)}s`

/** Groups databases into replication clusters: same Postgres system identifier, or linked by primary_id. */
export function clusters(dbs: Database[]) {
  const groups = new Map<string, Database[]>()
  for (const d of dbs) {
    const key = d.system_id || (d.primary_id ? `p:${d.primary_id}` : `p:${d.id}`)
    groups.set(key, [...(groups.get(key) ?? []), d])
  }
  // managed replicas still provisioning have no system id yet: attach them to their primary's group
  for (const [key, members] of [...groups]) {
    if (!key.startsWith('p:')) continue
    const primaryId = key.slice(2)
    const target = [...groups.entries()].find(([k, m]) => k !== key && m.some((d) => d.id === primaryId))
    if (target) {
      target[1].push(...members)
      groups.delete(key)
    }
  }
  return [...groups.values()].map((members) => {
    const primaries = members.filter((d) => d.role !== 'replica')
    return {
      primary: primaries[0],
      extraPrimaries: primaries.slice(1),
      replicas: members.filter((d) => d.role === 'replica'),
    }
  })
}

/** The "why" behind an unhealthy database, from its own issues and its server's resources. */
export function likelyCause(d: Database): string | null {
  if (d.server_status !== 'online') return `Server ${d.server_name} is offline`
  const u = d.server_usage
  if (u && u.io_util > 80) return `High disk I/O on ${d.server_name} (${u.io_util.toFixed(0)}% busy)`
  if (u && u.cpu > 85) return `High CPU on ${d.server_name} (${u.cpu.toFixed(0)}%)`
  if (u && u.disk > 90) return `Disk nearly full on ${d.server_name} (${u.disk.toFixed(0)}%)`
  if (d.role === 'replica' && d.stats.receiver?.status !== 'streaming')
    return 'Replica is not connected to its primary — check network reachability and pg_hba.conf'
  if (d.status === 'down') return d.error || 'Connection failed'
  return null
}
