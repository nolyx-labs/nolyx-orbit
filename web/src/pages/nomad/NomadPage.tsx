import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { api } from '../../lib/api'
import { ago } from '../../lib/format'
import { opPath } from '../../lib/paths'
import { statusTone, useClusters } from './nomadHelpers'

export function NomadPage() {
  const { project } = useProject()
  const canAdmin = useCan('admin')
  const nav = useNavigate()
  const [cs] = useClusters()
  const [tab, setTab] = useState<'jobs' | 'allocations' | 'deployments'>('jobs')
  const [stopping, setStopping] = useState<{ cluster: string; job: string } | null>(null)
  return (
    <div className="page">
      <PageHead
        title="Nomad"
        sub="Advanced view: jobs, task groups, allocations and deployments. Services hide most of this."
      />
      <div className="tabs">
        {(['jobs', 'allocations', 'deployments'] as const).map((t) => (
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
      {!cs ? (
        <Loading />
      ) : !cs.length ? (
        <Panel>
          <Empty title="No Nomad clusters">See Infrastructure → Clusters.</Empty>
        </Panel>
      ) : (
        cs.map((c) => (
          <Panel key={c.id} title={`${c.region} / ${c.datacenter}`}>
            {tab === 'jobs' &&
              (!c.jobs.length ? (
                <Empty title="No jobs" />
              ) : (
                <table className="t">
                  <thead>
                    <tr>
                      <th>Job</th>
                      <th>Type</th>
                      <th>Status</th>
                      <th>Task groups</th>
                      <th>Managed by Orbit</th>
                      <th />
                    </tr>
                  </thead>
                  <tbody>
                    {c.jobs.map((j) => {
                      const groups = Object.entries<any>(j.JobSummary?.Summary ?? {})
                      return (
                        <tr key={j.ID}>
                          <td className="name">{j.Name}</td>
                          <td className="muted">{j.Type}</td>
                          <td>
                            <StatusText tone={statusTone(j.Status)}>{j.Status}</StatusText>
                          </td>
                          <td className="mono" style={{ fontSize: 12 }}>
                            {groups
                              .map(
                                ([g, s]) =>
                                  `${g}: ${s.Running} running${s.Failed ? `, ${s.Failed} failed` : ''}${s.Queued ? `, ${s.Queued} queued` : ''}`,
                              )
                              .join(' · ') || '—'}
                          </td>
                          <td className="muted">{j.Meta?.['orbit.app'] ? 'yes' : '—'}</td>
                          <td className="right">
                            {canAdmin && j.Status !== 'dead' && (
                              <button
                                className="btn ghost sm danger"
                                onClick={() => setStopping({ cluster: c.id, job: j.ID })}
                              >
                                Stop
                              </button>
                            )}
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              ))}
            {tab === 'allocations' &&
              (!c.allocations.length ? (
                <Empty title="No allocations" />
              ) : (
                <table className="t">
                  <thead>
                    <tr>
                      <th>Allocation</th>
                      <th>Job</th>
                      <th>Task group</th>
                      <th>Node</th>
                      <th>Client status</th>
                      <th>Desired</th>
                      <th className="right">Created</th>
                    </tr>
                  </thead>
                  <tbody>
                    {c.allocations.slice(0, 200).map((a) => (
                      <tr key={a.ID}>
                        <td className="mono">{a.ID.slice(0, 8)}</td>
                        <td>{a.JobID}</td>
                        <td className="muted">{a.TaskGroup}</td>
                        <td className="muted">{a.NodeName}</td>
                        <td>
                          <StatusText tone={statusTone(a.ClientStatus)}>{a.ClientStatus}</StatusText>
                        </td>
                        <td className="muted">{a.DesiredStatus}</td>
                        <td className="right muted">{ago(new Date(a.CreateTime / 1e6).toISOString())}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              ))}
            {tab === 'deployments' &&
              (!c.deployments.length ? (
                <Empty title="No deployments" />
              ) : (
                <table className="t">
                  <thead>
                    <tr>
                      <th>Deployment</th>
                      <th>Job</th>
                      <th>Version</th>
                      <th>Status</th>
                      <th>Description</th>
                    </tr>
                  </thead>
                  <tbody>
                    {c.deployments.map((d) => (
                      <tr key={d.ID}>
                        <td className="mono">{d.ID.slice(0, 8)}</td>
                        <td>{d.JobID}</td>
                        <td className="mono">{d.JobVersion}</td>
                        <td>
                          <StatusText tone={statusTone(d.Status)}>{d.Status}</StatusText>
                        </td>
                        <td className="muted">{d.StatusDescription}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              ))}
          </Panel>
        ))
      )}
      {stopping && (
        <Confirm
          title={`Stop job ${stopping.job}?`}
          action="Stop job"
          danger
          typeToConfirm={stopping.job}
          onClose={() => setStopping(null)}
          onConfirm={() =>
            api<{ operation_id: string }>(
              'POST',
              `/projects/${project.id}/nomad/clusters/${stopping.cluster}/jobs/${stopping.job}/stop`,
              { confirm: stopping.job },
            ).then((r) => nav(opPath(project.id, r.operation_id)))
          }
        >
          Nomad stops every allocation of this job. Traffic to it stops.
        </Confirm>
      )}
    </div>
  )
}
