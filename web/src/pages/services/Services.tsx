import { Link, useNavigate } from 'react-router'
import { useCan, useProject } from '../../components/layout/context'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { useApi } from '../../lib/api'
import { bytes } from '../../lib/format'
import { useLive } from '../../lib/live'
import { App, appStatus } from './serviceHelpers'

export function Services() {
  const { project } = useProject()
  const nav = useNavigate()
  const canEdit = useCan('member')
  const [apps, , reload] = useApi<App[]>(`/projects/${project.id}/apps`)
  useLive(project.id, ['containers', 'deployments', 'nomad'], reload)
  return (
    <div className="page">
      <PageHead title="Services" sub="Applications deployed as containers, on one server or across a Nomad cluster">
        {canEdit && (
          <Link className="btn primary" to={`/p/${project.id}/services/new`}>
            New service
          </Link>
        )}
      </PageHead>
      <Panel>
        {!apps ? (
          <Loading />
        ) : !apps.length ? (
          <Empty title="No services yet">
            A service is an image (or a Git repository), ports, environment and a health check.
          </Empty>
        ) : (
          <table className="t">
            <thead>
              <tr>
                <th>Service</th>
                <th>Status</th>
                <th>Source</th>
                <th>Runs on</th>
                <th className="right">CPU</th>
                <th className="right">Memory</th>
                <th className="right">Last deploy</th>
              </tr>
            </thead>
            <tbody>
              {apps.map((a) => {
                const [tone, label] = appStatus(a)
                return (
                  <tr key={a.id} className="link" onClick={() => nav(`/p/${project.id}/services/${a.id}`)}>
                    <td>
                      <div className="name">{a.name}</div>
                      <div className="sub mono">
                        {a.ports.join(', ') || 'no ports'}
                        {a.proxy && ' · zero-downtime'}
                      </div>
                    </td>
                    <td>
                      <StatusText tone={tone}>
                        {a.orchestrator === 'nomad' && label === 'not deployed' ? 'Nomad' : label}
                      </StatusText>
                    </td>
                    <td className="mono muted" style={{ maxWidth: 300, overflow: 'hidden', textOverflow: 'ellipsis' }}>
                      {a.repo_url ? `${a.repo_url.replace('https://github.com/', '')}@${a.branch || 'main'}` : a.image}
                    </td>
                    <td className="muted">
                      {a.orchestrator === 'nomad'
                        ? `Nomad · ${a.instances} instance${a.instances > 1 ? 's' : ''}`
                        : (a.server_name ?? '—')}
                    </td>
                    <td className="right mono">
                      {a.container?.state === 'running' ? `${a.container.cpu.toFixed(1)}%` : '—'}
                    </td>
                    <td className="right mono">
                      {a.container?.state === 'running' ? bytes(a.container.mem_used) : '—'}
                    </td>
                    <td className="right mono muted">
                      {a.last_deployment ? `#${a.last_deployment.number} ${a.last_deployment.status}` : '—'}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </Panel>
    </div>
  )
}
