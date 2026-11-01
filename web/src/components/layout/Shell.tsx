import { Link, Navigate, Outlet, useMatch, useParams } from 'react-router'
import { useApi } from '../../lib/api'
import { PageHead } from '../ui/PageHead'
import { Panel } from '../ui/Panel'
import { ErrorNote } from '../ui/states'
import { Me, ProjectCtx } from './context'
import { Sidebar } from './Sidebar'

export function Shell() {
  const { pid } = useParams()
  const [me, error, reloadMe] = useApi<Me>('/me')
  const onAccount = useMatch('/p/:pid/settings/account')
  if (error)
    return (
      <div className="page">
        <ErrorNote error={error} />
      </div>
    )
  if (!me) return null
  const org = me.orgs.find((o) => o.projects.some((p) => p.id === pid))
  const project = org?.projects.find((p) => p.id === pid)
  if (!org || !project) {
    const first = me.orgs[0]?.projects[0]
    return first ? <Navigate to={`/p/${first.id}/overview`} replace /> : <div className="page">No projects.</div>
  }
  return (
    <ProjectCtx.Provider value={{ me, org, project, reloadMe }}>
      <div className="shell">
        <Sidebar />
        <main className="main">
          {org.mfa_blocked && !onAccount ? (
            <div className="page">
              <PageHead title="Two-factor authentication required" />
              <Panel pad>
                <div className="stack">
                  <div>
                    {org.name} requires two-factor authentication. Turn it on for your account to keep working in this
                    organization.
                  </div>
                  <div>
                    <Link className="btn primary" to={`/p/${project.id}/settings/account`}>
                      Set up two-factor authentication
                    </Link>
                  </div>
                </div>
              </Panel>
            </div>
          ) : (
            <Outlet />
          )}
        </main>
      </div>
    </ProjectCtx.Provider>
  )
}
