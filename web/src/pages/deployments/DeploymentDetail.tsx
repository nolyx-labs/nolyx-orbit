import { useEffect, useRef } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useProject } from '../../components/layout/context'
import { StepList } from '../../components/Progress'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { useApi } from '../../lib/api'
import { datetime, duration } from '../../lib/format'
import { useLive } from '../../lib/live'
import { Deployment } from '../../lib/types'
import { deployTone } from './DeploymentRows'

interface DeploymentDetailData extends Deployment {
  steps: Record<string, 'running' | 'done' | 'failed'>
  step_names: string[]
  log: string
  server_id: string | null
  containers: { id: string; name: string; state: string; status: string }[]
}

export function DeploymentDetail() {
  const { project } = useProject()
  const { did } = useParams()
  const [d, error, reload] = useApi<DeploymentDetailData>(`/projects/${project.id}/deployments/${did}`)
  useLive(project.id, ['deployments'], reload, 500)
  const logRef = useRef<HTMLPreElement>(null)
  useEffect(() => {
    logRef.current?.scrollTo(0, logRef.current.scrollHeight)
  }, [d?.log])

  if (error)
    return (
      <div className="page">
        <Empty title={error} />
      </div>
    )
  if (!d)
    return (
      <div className="page">
        <Loading />
      </div>
    )
  return (
    <div className="page">
      <PageHead
        crumbs={
          <>
            <Link to={`/p/${project.id}/deployments`}>Deployments</Link> /{' '}
            <Link to={`/p/${project.id}/services/${d.app_id}`}>{d.app}</Link>
          </>
        }
        title={
          <>
            {d.status === 'running' ? 'Deploying' : 'Deployment of'} {d.app}{' '}
            <span className="faint mono" style={{ fontWeight: 400 }}>
              #{d.number}
            </span>
          </>
        }
        sub={
          <StatusText tone={deployTone(d.status)}>
            {d.status === 'running' ? 'In progress' : d.status === 'succeeded' ? 'Successful' : 'Failed'}
          </StatusText>
        }
      />
      <div className="split">
        <div className="stack" style={{ gap: 12 }}>
          {d.error && (
            <div className="callout err">
              <b style={{ fontWeight: 500 }}>Deployment failed:</b> <span className="mono">{d.error}</span>
              {d.status === 'failed' && (
                <div className="muted" style={{ marginTop: 4 }}>
                  The previous container (if any) was restarted.
                </div>
              )}
            </div>
          )}
          <Panel title="Progress">
            <div className="panel-body">
              <StepList plan={d.step_names} steps={d.steps} status={d.status} />
            </div>
          </Panel>
          <Panel title={d.step_names[0] === 'Building image' ? 'Build & deployment log' : 'Deployment log'}>
            <div className="panel-body">
              <pre ref={logRef} className="code logs" style={{ maxHeight: 460 }}>
                {d.log || 'Waiting for agent…'}
              </pre>
            </div>
          </Panel>
        </div>
        <Panel title="Details">
          <div className="panel-body kv">
            <div className="k">Deployment</div>
            <div className="mono">#{d.number}</div>
            <div className="k">Service</div>
            <div>
              <Link to={`/p/${project.id}/services/${d.app_id}`}>{d.app}</Link>
            </div>
            <div className="k">Image</div>
            <div className="mono" style={{ wordBreak: 'break-all' }}>
              {d.image}
            </div>
            <div className="k">Server</div>
            <div>
              {d.server_id ? <Link to={`/p/${project.id}/servers/${d.server_id}/overview`}>{d.server_name}</Link> : '—'}
            </div>
            <div className="k">Triggered by</div>
            <div>{d.triggered_by ?? '—'}</div>
            <div className="k">Started</div>
            <div>{datetime(d.created_at)}</div>
            <div className="k">Duration</div>
            <div className="mono">{duration(d.duration)}</div>
            <div className="k">Commit</div>
            <div>
              {d.commit_sha ? (
                <>
                  <span className="mono">{d.commit_sha.slice(0, 12)}</span>{' '}
                  <span className="muted">{d.commit_message}</span>
                </>
              ) : (
                <span className="faint">—</span>
              )}
            </div>
            {d.commit_author && (
              <>
                <div className="k">Author</div>
                <div>{d.commit_author}</div>
              </>
            )}
            {d.branch && (
              <>
                <div className="k">Branch</div>
                <div className="mono">{d.branch}</div>
              </>
            )}
            <div className="k">Runs on</div>
            <div>{d.orchestrator === 'nomad' ? 'Nomad (rolling update)' : 'Docker'}</div>
            <div className="k">Container</div>
            <div className="mono" style={{ fontSize: 12 }}>
              {d.containers?.length ? (
                d.containers.map((c) => `${c.name} · ${c.state}`).join(', ')
              ) : (
                <span className="faint">removed or not created</span>
              )}
            </div>
          </div>
        </Panel>
      </div>
    </div>
  )
}
