import { useProject } from '../../components/layout/context'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Loading } from '../../components/ui/states'
import { useApi } from '../../lib/api'
import { useLive } from '../../lib/live'
import { Deployment } from '../../lib/types'
import { DeploymentRows } from './DeploymentRows'

export function Deployments() {
  const { project } = useProject()
  const [deps, , reload] = useApi<Deployment[]>(`/projects/${project.id}/deployments`)
  useLive(project.id, ['deployments'], reload)
  return (
    <div className="page">
      <PageHead title="Deployments" sub="Every deploy across all services, including Git pushes" />
      <Panel>{deps ? <DeploymentRows deployments={deps} /> : <Loading />}</Panel>
    </div>
  )
}
