import { useState } from 'react'
import { Link } from 'react-router'
import { useCan, useProject } from '../../../components/layout/context'
import { Confirm } from '../../../components/ui/Confirm'
import { PageHead } from '../../../components/ui/PageHead'
import { Panel } from '../../../components/ui/Panel'
import { Empty, Loading } from '../../../components/ui/states'
import { Badge, StatusText } from '../../../components/ui/status'
import { api, useApi } from '../../../lib/api'
import { ago } from '../../../lib/format'
import { useLive } from '../../../lib/live'
import { incidentLink } from '../../../lib/paths'
import { Channel, Incident, METRICS, Rule } from './alertMetrics'
import { NewAlert } from './NewAlertDialog'
import { NewChannel } from './NewChannelDialog'

export function Alerts() {
  const { project } = useProject()
  const canEdit = useCan('member')
  const [data, , reload] = useApi<{ rules: Rule[]; incidents: Incident[] }>(`/projects/${project.id}/alerts`)
  const [ch, , reloadCh] = useApi<{ channels: Channel[]; email_configured: boolean }>(
    `/projects/${project.id}/channels`,
  )
  const [creating, setCreating] = useState(false)
  const [addingChannel, setAddingChannel] = useState(false)
  const [deleting, setDeleting] = useState<Rule | null>(null)
  useLive(project.id, ['alerts'], reload)
  const chName = (id: string) => ch?.channels.find((c) => c.id === id)?.name ?? '?'
  return (
    <div className="page">
      <PageHead
        title="Alerts"
        sub="Evaluated every 30 seconds against every server, database and service in the project"
      >
        {canEdit && (
          <button className="btn primary" onClick={() => setCreating(true)}>
            New alert
          </button>
        )}
      </PageHead>
      <div className="stack">
        <Panel title="Incidents">
          {!data ? (
            <Loading />
          ) : !data.incidents.length ? (
            <Empty title="No incidents" />
          ) : (
            <div className="list">
              {data.incidents.map((i) => (
                <div key={i.id} className="list-item">
                  <StatusText tone={i.resolved_at ? '' : i.acknowledged_at ? 'warn' : 'err'}>
                    {i.resolved_at ? 'Resolved' : i.acknowledged_at ? 'Acknowledged' : 'Firing'}
                  </StatusText>
                  <Link to={incidentLink(project.id, i)} style={{ fontWeight: 500 }}>
                    {i.rule}
                  </Link>
                  <span className="muted">
                    on {i.target_type} {i.target_name}
                  </span>
                  {METRICS[i.metric]?.unit && (
                    <span className="mono faint">
                      {i.value.toFixed(1)}
                      {METRICS[i.metric].unit}
                    </span>
                  )}
                  <span className="spacer" />
                  <span className="faint" style={{ fontSize: 12 }}>
                    {ago(i.opened_at)}
                    {i.resolved_at && ` · resolved ${ago(i.resolved_at)}`}
                    {i.acknowledged_by && ` · ack ${i.acknowledged_by}`}
                  </span>
                  {canEdit && !i.resolved_at && !i.acknowledged_at && (
                    <button
                      className="btn ghost sm"
                      onClick={() => api('POST', `/projects/${project.id}/incidents/${i.id}/ack`).then(reload)}
                    >
                      Acknowledge
                    </button>
                  )}
                </div>
              ))}
            </div>
          )}
        </Panel>
        <Panel title="Rules">
          {!data ? (
            <Loading />
          ) : !data.rules.length ? (
            <Empty title="No alert rules">Example: IF CPU &gt; 90% for 5 minutes THEN notify #ops.</Empty>
          ) : (
            <table className="t">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Condition</th>
                  <th>Notify</th>
                  <th>State</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {data.rules.map((r) => {
                  const m = METRICS[r.metric]
                  return (
                    <tr key={r.id}>
                      <td className="name">{r.name}</td>
                      <td className="mono muted">
                        IF {m?.label ?? r.metric}
                        {m?.unit && ` > ${r.threshold}${m.unit}`}
                        {r.metric !== 'deployment_failed' &&
                          r.metric !== 'container_restarts' &&
                          ` for ${r.duration_minutes}m`}
                      </td>
                      <td className="muted">
                        {[...r.channel_ids.map(chName), ...(r.has_webhook ? ['webhook'] : [])].join(', ') ||
                          'Event only'}
                      </td>
                      <td>
                        {r.firing ? (
                          <StatusText tone="err">Firing on {r.firing}</StatusText>
                        ) : (
                          <StatusText tone="ok">OK</StatusText>
                        )}
                      </td>
                      <td className="right">
                        {canEdit && (
                          <button className="btn ghost sm" onClick={() => setDeleting(r)}>
                            Delete
                          </button>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          )}
        </Panel>
        <Panel
          title="Notification channels"
          actions={
            canEdit && (
              <button className="btn sm" onClick={() => setAddingChannel(true)}>
                Add channel
              </button>
            )
          }
        >
          {!ch ? (
            <Loading />
          ) : !ch.channels.length ? (
            <Empty title="No channels">Email, Slack, Discord or any webhook.</Empty>
          ) : (
            <div className="list">
              {ch.channels.map((c) => (
                <div key={c.id} className="list-item">
                  <span style={{ fontWeight: 500 }}>{c.name}</span>
                  <Badge>{c.type}</Badge>
                  <span className="spacer" />
                  {canEdit && (
                    <button
                      className="btn ghost sm danger"
                      onClick={() =>
                        api('DELETE', `/projects/${project.id}/channels/${c.id}`).then(() => {
                          reloadCh()
                          reload()
                        })
                      }
                    >
                      Remove
                    </button>
                  )}
                </div>
              ))}
            </div>
          )}
        </Panel>
      </div>
      {creating && <NewAlert channels={ch?.channels ?? []} onClose={() => setCreating(false)} onSaved={reload} />}
      {addingChannel && (
        <NewChannel
          emailConfigured={!!ch?.email_configured}
          onClose={() => setAddingChannel(false)}
          onSaved={reloadCh}
        />
      )}
      {deleting && (
        <Confirm
          title={`Delete alert "${deleting.name}"?`}
          action="Delete"
          danger
          onClose={() => setDeleting(null)}
          onConfirm={() => api('DELETE', `/projects/${project.id}/alerts/${deleting.id}`).then(reload)}
        >
          Open incidents for this rule are closed and its history is removed.
        </Confirm>
      )}
    </div>
  )
}
