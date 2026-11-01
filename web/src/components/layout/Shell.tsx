import { Navigate, Outlet, useParams } from 'react-router-dom'
import { useApi } from '../../lib/api'
import { ErrorNote } from '../ui/states'
import { Me, ProjectCtx } from './context'
import { Sidebar } from './Sidebar'

export function Shell() {
  const { pid } = useParams()
  const [me, error, reloadMe] = useApi<Me>('/me')
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
          <Outlet />
        </main>
      </div>
    </ProjectCtx.Provider>
  )
}
