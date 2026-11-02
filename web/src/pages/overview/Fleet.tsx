import { useNavigate } from 'react-router'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { useApi } from '../../lib/api'

interface FleetRow {
  org_id: string
  org: string
  id: string
  name: string
  servers: number
  online: number
  apps: number
  databases: number
  incidents: number
  attention: number
}

/** Every project I can see, across organizations, so agencies can watch all their clients at once. */
export function Fleet() {
  const nav = useNavigate()
  const [rows] = useApi<FleetRow[]>('/fleet')
  const orgs = [...new Set(rows?.map((r) => r.org_id))]
  return (
    <div className="page">
      <PageHead title="All projects" sub="Every project in every organization you belong to" />
      {!rows ? (
        <Loading />
      ) : !rows.length ? (
        <Empty title="No projects" />
      ) : (
        <div className="stack">
          {orgs.map((oid) => {
            const projects = rows.filter((r) => r.org_id === oid)
            return (
              <Panel key={oid} title={projects[0].org}>
                <table className="t">
                  <thead>
                    <tr>
                      <th>Project</th>
                      <th>Health</th>
                      <th className="right">Servers</th>
                      <th className="right">Services</th>
                      <th className="right">Databases</th>
                    </tr>
                  </thead>
                  <tbody>
                    {projects.map((p) => {
                      const issues = p.servers - p.online + p.incidents + p.attention
                      return (
                        <tr key={p.id} className="link" onClick={() => nav(`/p/${p.id}/overview`)}>
                          <td className="name">{p.name}</td>
                          <td>
                            {p.servers === 0 ? (
                              <StatusText tone="">No servers</StatusText>
                            ) : issues ? (
                              <StatusText tone="warn">
                                {issues} issue{issues > 1 ? 's' : ''}
                              </StatusText>
                            ) : (
                              <StatusText tone="ok">Healthy</StatusText>
                            )}
                          </td>
                          <td className="right mono">
                            {p.online}/{p.servers}
                          </td>
                          <td className="right mono">{p.apps}</td>
                          <td className="right mono">{p.databases}</td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </Panel>
            )
          })}
        </div>
      )}
    </div>
  )
}
