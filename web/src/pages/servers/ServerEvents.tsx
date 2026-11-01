import { useProject } from '../../components/layout/context'
import { EventList } from '../../components/ui/EventList'
import { Panel } from '../../components/ui/Panel'
import { Loading } from '../../components/ui/states'
import { useApi } from '../../lib/api'
import { useLive } from '../../lib/live'
import { OrbitEvent } from '../../lib/types'

export function ServerEvents({ sid }: { sid: string }) {
  const { project } = useProject()
  const [events, , reload] = useApi<OrbitEvent[]>(`/projects/${project.id}/events?server=${sid}`)
  useLive(project.id, ['events'], reload)
  return <Panel>{events ? <EventList events={events} /> : <Loading />}</Panel>
}
