import { useState } from 'react'
import { useProject } from '../../components/layout/context'
import { EventList } from '../../components/ui/EventList'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Loading } from '../../components/ui/states'
import { useApi } from '../../lib/api'
import { useLive } from '../../lib/live'
import { OrbitEvent } from '../../lib/types'

export function Events() {
  const { project } = useProject()
  const [events, , reload] = useApi<OrbitEvent[]>(`/projects/${project.id}/events`)
  const [sev, setSev] = useState('')
  useLive(project.id, ['events'], reload)
  const shown = events?.filter((e) => !sev || e.severity === sev)
  return (
    <div className="page">
      <PageHead title="Events" sub="What changed, and when">
        <select
          className="select"
          style={{ width: 140 }}
          value={sev}
          onChange={(e) => setSev(e.target.value)}
          aria-label="Severity"
        >
          <option value="">All severities</option>
          <option value="info">Info</option>
          <option value="warning">Warning</option>
          <option value="error">Error</option>
        </select>
      </PageHead>
      <Panel>{shown ? <EventList events={shown} showServer /> : <Loading />}</Panel>
    </div>
  )
}
