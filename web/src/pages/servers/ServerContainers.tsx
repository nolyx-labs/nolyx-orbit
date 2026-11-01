import { ContainersTable } from '../../components/containers/ContainersTable'
import { useProject } from '../../components/layout/context'
import { Panel } from '../../components/ui/Panel'
import { Loading } from '../../components/ui/states'
import { useApi } from '../../lib/api'
import { useLive } from '../../lib/live'
import { Container } from '../../lib/types'

export function ServerContainers({ sid }: { sid: string }) {
  const { project } = useProject()
  const [cs, , reload] = useApi<Container[]>(`/projects/${project.id}/containers?server=${sid}`)
  useLive(project.id, ['containers'], reload)
  return <Panel>{cs ? <ContainersTable containers={cs} onChanged={reload} /> : <Loading />}</Panel>
}
