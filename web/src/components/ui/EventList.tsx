import { clock } from '../../lib/format'
import { OrbitEvent } from '../../lib/types'
import { Empty } from './states'
import { Dot, Tone } from './status'

const sevTone: Record<OrbitEvent['severity'], Tone> = { info: '', warning: 'warn', error: 'err' }

export function EventList({ events, showServer }: { events: OrbitEvent[]; showServer?: boolean }) {
  if (!events.length)
    return <Empty title="No events yet">Connections, deployments, restarts and alerts appear here.</Empty>
  return (
    <div className="list timeline">
      {events.map((e) => (
        <div key={e.id} className="list-item">
          <span className="time" title={new Date(e.created_at).toLocaleString()}>
            {clock(e.created_at)}
          </span>
          <Dot tone={sevTone[e.severity]} />
          <span style={{ flex: 1, minWidth: 0 }}>{e.message}</span>
          {showServer && e.server_name && <span className="faint nowrap">{e.server_name}</span>}
        </div>
      ))}
    </div>
  )
}
