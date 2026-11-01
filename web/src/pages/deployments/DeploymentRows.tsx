import { Link } from 'react-router-dom'
import { useProject } from '../../components/layout/context'
import { Empty } from '../../components/ui/states'
import { StatusText, Tone } from '../../components/ui/status'
import { ago, duration } from '../../lib/format'
import { Deployment } from '../../lib/types'

export const deployTone = (s: Deployment['status']): Tone =>
  s === 'succeeded' ? 'ok' : s === 'failed' ? 'err' : 'info'

export function DeploymentRows({ deployments }: { deployments: Deployment[] }) {
  const { project } = useProject()
  if (!deployments.length) return <Empty title="No deployments yet">Define a service and deploy it.</Empty>
  return (
    <div className="list">
      {deployments.map((d) => (
        <Link key={d.id} className="list-item" to={`/p/${project.id}/deployments/${d.id}`}>
          <span className="mono faint" style={{ width: 44 }}>
            #{d.number}
          </span>
          <span style={{ width: 120, fontWeight: 500 }}>{d.app}</span>
          <span
            className="mono muted"
            style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
          >
            {d.commit_sha ? (
              <>
                {d.commit_sha.slice(0, 7)} <span className="faint">{d.commit_message}</span>
              </>
            ) : (
              d.image
            )}
          </span>
          <StatusText tone={deployTone(d.status)}>{d.status}</StatusText>
          <span className="mono faint" style={{ width: 56, textAlign: 'right' }}>
            {duration(d.duration)}
          </span>
          <span className="faint" style={{ width: 70, textAlign: 'right' }}>
            {ago(d.created_at)}
          </span>
        </Link>
      ))}
    </div>
  )
}
