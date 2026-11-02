import { Link } from 'react-router'
import { useProject } from '../../components/layout/context'
import { Dot } from '../../components/ui/status'
import { ago } from '../../lib/format'
import { incidentLink } from '../../lib/paths'
import { OverviewData } from './Overview'

export type AttentionKind = 'backup_failed' | 'findings' | 'monitor_down' | 'reboot' | 'security_updates'

// What each attention row says and where it leads (id is a server, or a monitor for monitor_down).
const KINDS: Record<AttentionKind, { tone: 'err' | 'warn'; text: (n: number) => string; tab: string; action: string }> =
  {
    monitor_down: { tone: 'err', text: () => 'monitor is down', tab: '', action: 'View monitor' },
    backup_failed: { tone: 'err', text: () => 'last backup failed', tab: 'backups', action: 'View backups' },
    findings: {
      tone: 'warn',
      text: (n) => `${n} high-severity security finding${n > 1 ? 's' : ''}`,
      tab: 'security',
      action: 'View findings',
    },
    security_updates: {
      tone: 'warn',
      text: (n) => `${n} security update${n > 1 ? 's' : ''} pending`,
      tab: 'updates',
      action: 'View updates',
    },
    reboot: { tone: 'warn', text: () => 'needs a reboot to finish updates', tab: 'updates', action: 'View updates' },
  }

export function Attention({ data }: { data: OverviewData }) {
  const { project } = useProject()
  return (
    <div className="panel" style={{ marginTop: 12, borderColor: 'color-mix(in srgb, var(--warn) 35%, var(--border))' }}>
      <div className="panel-head">
        <h3>Needs attention</h3>
      </div>
      <div className="list">
        {data.incidents.map((i) => (
          <Link key={i.id} className="list-item" to={incidentLink(project.id, i)}>
            <Dot tone="err" />
            <span style={{ flex: 1 }}>
              <b style={{ fontWeight: 500 }}>{i.rule}</b> firing on {i.target_type} {i.target_name}
              <span className="muted">
                {' '}
                — {i.metric.replace(/_/g, ' ')} {i.value ? i.value.toFixed(1) : ''}
              </span>
            </span>
            <span className="faint">since {ago(i.opened_at)}</span>
            <span className="btn sm">View</span>
          </Link>
        ))}
        {data.attention.map((a) => {
          const k = KINDS[a.kind]
          return (
            <Link
              key={a.kind + a.id}
              className="list-item"
              to={
                a.kind === 'monitor_down'
                  ? `/p/${project.id}/monitors/${a.id}`
                  : `/p/${project.id}/servers/${a.id}/${k.tab}`
              }
            >
              <Dot tone={k.tone} />
              <span style={{ flex: 1 }}>
                <b style={{ fontWeight: 500 }}>{a.name}</b> <span className="muted">— {k.text(a.n)}</span>
              </span>
              <span className="btn sm">{k.action}</span>
            </Link>
          )
        })}
        {data.servers.total - data.servers.online > 0 && (
          <Link className="list-item" to={`/p/${project.id}/servers`}>
            <Dot tone="err" />
            <span style={{ flex: 1 }}>
              {data.servers.total - data.servers.online} server(s) offline{' '}
              <span className="muted">
                — the agent is not connected. Check the agent service with{' '}
                <span className="mono">journalctl -u orbit-agent</span>.
              </span>
            </span>
            <span className="btn sm">View servers</span>
          </Link>
        )}
        {data.containers.unhealthy > 0 && (
          <Link className="list-item" to={`/p/${project.id}/docker`}>
            <Dot tone="err" />
            <span style={{ flex: 1 }}>
              {data.containers.unhealthy} container(s) unhealthy or restarting{' '}
              <span className="muted">— open a container to see its logs.</span>
            </span>
            <span className="btn sm">View containers</span>
          </Link>
        )}
        {data.databases.unhealthy > 0 && (
          <Link className="list-item" to={`/p/${project.id}/replicas`}>
            <Dot tone="warn" />
            <span style={{ flex: 1 }}>
              {data.databases.unhealthy} database(s) degraded or down{' '}
              <span className="muted">— open Replicas for lag and likely causes.</span>
            </span>
            <span className="btn sm">View databases</span>
          </Link>
        )}
        {data.apps.total - data.apps.healthy > 0 && (
          <Link className="list-item" to={`/p/${project.id}/services`}>
            <Dot tone="warn" />
            <span style={{ flex: 1 }}>
              {data.apps.total - data.apps.healthy} service(s) not running{' '}
              <span className="muted">— never deployed, stopped, or failing health checks.</span>
            </span>
            <span className="btn sm">View services</span>
          </Link>
        )}
      </div>
    </div>
  )
}
