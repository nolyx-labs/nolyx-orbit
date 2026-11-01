import { Link, useParams } from 'react-router'
import { useProject } from '../../components/layout/context'
import { LogBox, StepList } from '../../components/Progress'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { useApi } from '../../lib/api'
import { datetime, duration } from '../../lib/format'
import { useLive } from '../../lib/live'
import type { Operation } from '../../lib/types'

export const OP_LABEL: Record<string, string> = {
  'db.provision': 'Provision PostgreSQL',
  'db.replica.create': 'Create replica',
  'db.resync': 'Re-sync replica',
  'db.remove': 'Remove database',
  'db.promote': 'Promote replica',
  'db.backup': 'Backup',
  'db.restore': 'Restore',
  'nomad.install': 'Install Nomad',
  'nomad.stop': 'Stop Nomad job',
}

export function OperationPage() {
  const { project } = useProject()
  const { oid } = useParams()
  const [op, error, reload] = useApi<Operation>(`/projects/${project.id}/operations/${oid}`)
  useLive(project.id, ['operations'], reload, 500)
  if (error)
    return (
      <div className="page">
        <Empty title={error} />
      </div>
    )
  if (!op)
    return (
      <div className="page">
        <Loading />
      </div>
    )
  const tone = op.status === 'succeeded' ? 'ok' : op.status === 'failed' ? 'err' : 'info'
  return (
    <div className="page">
      <PageHead
        crumbs={
          op.database_id ? (
            <Link to={`/p/${project.id}/databases/${op.database_id}`}>Database</Link>
          ) : (
            <Link to={`/p/${project.id}/events`}>Events</Link>
          )
        }
        title={
          <>
            {OP_LABEL[op.kind] ?? op.kind}{' '}
            <span className="faint" style={{ fontWeight: 400 }}>
              {op.target}
            </span>
          </>
        }
        sub={
          <StatusText tone={tone}>
            {op.status === 'running' ? 'In progress' : op.status === 'succeeded' ? 'Succeeded' : 'Failed'}
          </StatusText>
        }
      />
      <div className="split">
        <div className="stack" style={{ gap: 12 }}>
          {op.error && (
            <div className="callout err">
              <pre className="mono" style={{ margin: 0, whiteSpace: 'pre-wrap' }}>
                {op.error}
              </pre>
            </div>
          )}
          <Panel title="Progress">
            <div className="panel-body">
              <StepList plan={op.plan} steps={op.steps} status={op.status} />
            </div>
          </Panel>
          <Panel title="Log">
            <div className="panel-body">
              <LogBox log={op.log} empty={op.status === 'running' ? 'Waiting for agent…' : 'No log output.'} />
            </div>
          </Panel>
        </div>
        <Panel title="Details">
          <div className="panel-body kv">
            <div className="k">Operation</div>
            <div>{OP_LABEL[op.kind] ?? op.kind}</div>
            <div className="k">Target</div>
            <div className="mono">{op.target}</div>
            <div className="k">Server</div>
            <div>
              {op.server_id ? (
                <Link to={`/p/${project.id}/servers/${op.server_id}/overview`}>{op.server_name}</Link>
              ) : (
                '—'
              )}
            </div>
            <div className="k">Triggered by</div>
            <div>{op.triggered_by ?? 'scheduler'}</div>
            <div className="k">Started</div>
            <div>{datetime(op.created_at)}</div>
            <div className="k">Duration</div>
            <div className="mono">{duration(op.duration)}</div>
          </div>
        </Panel>
      </div>
    </div>
  )
}
