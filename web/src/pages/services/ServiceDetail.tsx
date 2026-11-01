import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Chart } from '../../components/charts/Chart'
import { useCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { Copy, RangePicker } from '../../components/ui/controls'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { Badge, StatusText } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { bytes } from '../../lib/format'
import { useLive } from '../../lib/live'
import { Deployment, Series } from '../../lib/types'
import { DeploymentRows } from '../deployments/DeploymentRows'
import { EditService } from './EditServiceDialog'
import { App, appStatus, useNomadAllocs } from './serviceHelpers'

export function ServiceDetail() {
  const { project } = useProject()
  const { aid } = useParams()
  const nav = useNavigate()
  const canEdit = useCan('member')
  const canAdmin = useCan('admin')
  const [a, error, reload] = useApi<App>(`/projects/${project.id}/apps/${aid}`)
  const [deps, , reloadDeps] = useApi<Deployment[]>(`/projects/${project.id}/deployments?app=${aid}`)
  const [range, setRange] = useState('1h')
  const [m, , reloadM] = useApi<Series>(
    a?.proxy ? `/projects/${project.id}/app-metrics?app=${aid}&range=${range}` : null,
  )
  const allocs = useNomadAllocs(a)
  const [deploying, setDeploying] = useState(false)
  const [editing, setEditing] = useState(false)
  const [deleting, setDeleting] = useState(false)
  useLive(project.id, ['containers', 'deployments', 'nomad'], () => {
    reload()
    reloadDeps()
  })
  useLive(project.id, ['metrics'], reloadM, 10000)

  if (error)
    return (
      <div className="page">
        <Empty title={error} />
      </div>
    )
  if (!a)
    return (
      <div className="page">
        <Loading />
      </div>
    )
  const [tone, label] = appStatus(a, allocs)
  const nomad = a.orchestrator === 'nomad'
  return (
    <div className="page">
      <PageHead
        crumbs={<Link to={`/p/${project.id}/services`}>Services</Link>}
        title={a.name}
        sub={
          <StatusText tone={tone}>
            {label}
            {a.container && !nomad && <span className="faint"> · {a.container.status}</span>}
          </StatusText>
        }
      >
        {canEdit && (
          <button className="btn" onClick={() => setEditing(true)}>
            Edit
          </button>
        )}
        {canEdit && (
          <button className="btn primary" onClick={() => setDeploying(true)}>
            Deploy
          </button>
        )}
      </PageHead>
      {a.proxy && (
        <div className="stack" style={{ gap: 8, marginBottom: 16 }}>
          <div className="row">
            <span className="section-title" style={{ margin: 0 }}>
              Traffic
            </span>
            <div className="spacer" />
            <RangePicker value={range} onChange={setRange} />
          </div>
          {m && (
            <div className="charts" style={{ gridTemplateColumns: 'repeat(3, 1fr)' }}>
              <Chart
                title="Request rate"
                t={m.t}
                series={[{ label: 'req/s', values: m.rps, color: 'series-1' }]}
                format={(v) => `${v.toFixed(1)}/s`}
                height={130}
              />
              <Chart
                title="Error rate (5xx)"
                t={m.t}
                series={[{ label: 'errors', values: m.error_rate, color: 'series-1' }]}
                format={(v) => `${v.toFixed(2)}%`}
                height={130}
              />
              <Chart
                title="Latency p95"
                t={m.t}
                series={[{ label: 'p95', values: m.p95, color: 'series-1' }]}
                format={(v) => `${v.toFixed(0)}ms`}
                height={130}
              />
            </div>
          )}
        </div>
      )}
      <div className="split">
        <Panel title="Deployments">
          <DeploymentRows deployments={deps ?? []} />
        </Panel>
        <div className="stack" style={{ gap: 12 }}>
          <Panel title="Configuration">
            <div className="panel-body kv">
              {a.repo_url ? (
                <>
                  <div className="k">Repository</div>
                  <div className="mono" style={{ wordBreak: 'break-all' }}>
                    {a.repo_url}@{a.branch || 'main'}
                    {a.has_repo_token && <span className="faint"> · token set</span>}
                  </div>
                </>
              ) : (
                <>
                  <div className="k">Image</div>
                  <div className="mono" style={{ wordBreak: 'break-all' }}>
                    {a.image}
                  </div>
                </>
              )}
              <div className="k">Runs on</div>
              <div>
                {nomad ? (
                  `Nomad · ${a.instances} instance(s)`
                ) : a.server_id ? (
                  <Link to={`/p/${project.id}/servers/${a.server_id}/overview`}>{a.server_name}</Link>
                ) : (
                  '—'
                )}
              </div>
              <div className="k">Ports</div>
              <div className="mono">{a.ports.join(', ') || '—'}</div>
              <div className="k">Health check</div>
              <div className="mono">{a.health_path || 'container running'}</div>
              <div className="k">Resources</div>
              <div className="mono">
                {a.cpus ? `${a.cpus} CPU` : 'no CPU limit'} · {a.memory_mb ? `${a.memory_mb} MB` : 'no memory limit'}
              </div>
              <div className="k">Strategy</div>
              <div className="muted">
                {nomad
                  ? 'Nomad rolling update, auto-revert'
                  : a.proxy
                    ? 'zero-downtime: start → health check → switch traffic'
                    : 'stop → start → health check, auto-rollback'}
              </div>
            </div>
          </Panel>
          {a.repo_url && a.webhook_url && (
            <Panel
              title="GitHub webhook"
              actions={
                <Badge tone={a.auto_deploy ? 'ok' : ''}>
                  {a.auto_deploy ? `auto-deploy ${a.branch || 'main'}` : 'auto-deploy off'}
                </Badge>
              }
            >
              <div className="panel-body stack" style={{ gap: 8 }}>
                <div className="muted" style={{ fontSize: 12 }}>
                  Repository → Settings → Webhooks → Add webhook. Content type{' '}
                  <span className="mono">application/json</span>, "Just the push event".
                </div>
                <div className="field">
                  <span>Payload URL</span>
                  <div className="copy-row">
                    <pre className="code">{a.webhook_url}</pre>
                    <Copy text={a.webhook_url} />
                  </div>
                </div>
                {a.webhook_secret && (
                  <div className="field">
                    <span>Secret</span>
                    <div className="copy-row">
                      <pre className="code">{'•'.repeat(24)}</pre>
                      <Copy text={a.webhook_secret} />
                    </div>
                  </div>
                )}
              </div>
            </Panel>
          )}
          <Panel title="Environment">
            {a.env_keys.length ? (
              <div className="list">
                {a.env_keys.map((k) => (
                  <div key={k} className="list-item">
                    <span className="mono">{k}</span>
                    <span className="spacer" />
                    <span className="mono faint">••••••••</span>
                  </div>
                ))}
              </div>
            ) : (
              <Empty title="No variables" />
            )}
            <div className="panel-body faint" style={{ fontSize: 12, paddingTop: 0 }}>
              Plus organization and project <Link to={`/p/${project.id}/secrets`}>secrets</Link>.
            </div>
          </Panel>
          <Panel title="Instances">
            {nomad ? (
              !allocs.length ? (
                <Empty title="No allocations" />
              ) : (
                <div className="list">
                  {allocs.map((al) => (
                    <div key={al.ID} className="list-item">
                      <StatusText
                        tone={al.ClientStatus === 'running' ? 'ok' : al.ClientStatus === 'failed' ? 'err' : 'info'}
                      >
                        {al.NodeName}
                      </StatusText>
                      <span className="spacer" />
                      <span className="mono faint">
                        {al.ID.slice(0, 8)} · {al.ClientStatus}
                      </span>
                    </div>
                  ))}
                </div>
              )
            ) : a.container ? (
              <div className="list">
                <div className="list-item">
                  <StatusText tone={tone}>{a.server_name}</StatusText>
                  <span className="spacer" />
                  <span className="mono faint">
                    {a.container.id.slice(0, 12)} · {a.container.cpu.toFixed(1)}% · {bytes(a.container.mem_used)}
                  </span>
                </div>
              </div>
            ) : (
              <Empty title="Not running" />
            )}
            {nomad && (
              <div className="panel-body faint" style={{ fontSize: 12, paddingTop: 0 }}>
                Full Nomad details in <Link to={`/p/${project.id}/nomad`}>Nomad</Link>.
              </div>
            )}
          </Panel>
          <div className="row">
            <Link className="btn sm" to={`/p/${project.id}/logs?app=${a.name}`}>
              View logs
            </Link>
            <Link className="btn sm" to={`/p/${project.id}/releases`}>
              Releases
            </Link>
            <div className="spacer" />
            {canAdmin && (
              <button className="btn sm danger" onClick={() => setDeleting(true)}>
                Delete service
              </button>
            )}
          </div>
        </div>
      </div>
      {deploying && (
        <Confirm
          title={`Deploy ${a.name}?`}
          action="Deploy"
          onClose={() => setDeploying(false)}
          onConfirm={() =>
            api<{ id: string }>('POST', `/projects/${project.id}/apps/${a.id}/deploy`).then((d) =>
              nav(`/p/${project.id}/deployments/${d.id}`),
            )
          }
        >
          {a.repo_url ? (
            <>
              Builds the head of <span className="mono">{a.branch || 'main'}</span> on {a.server_name}
            </>
          ) : (
            <>
              Deploys <span className="mono">{a.image}</span>
            </>
          )}
          {nomad
            ? ' as a Nomad rolling update.'
            : a.proxy
              ? ', switches traffic once healthy and drains the previous container.'
              : '. The previous container is restarted if the health check fails.'}
          {!nomad && !a.proxy && (
            <div className="callout warn" style={{ marginTop: 10 }}>
              The old container stops first, so expect a few seconds of downtime. Enable zero-downtime deploys in Edit
              for HTTP services.
            </div>
          )}
        </Confirm>
      )}
      {editing && <EditService a={a} onClose={() => setEditing(false)} onSaved={reload} />}
      {deleting && (
        <Confirm
          title={`Delete ${a.name}?`}
          action="Delete service"
          danger
          typeToConfirm={a.name}
          onClose={() => setDeleting(false)}
          onConfirm={() =>
            api('DELETE', `/projects/${project.id}/apps/${a.id}`, { confirm: a.name }).then(() =>
              nav(`/p/${project.id}/services`),
            )
          }
        >
          Removes the service definition, its variables and deployment history. Running containers or Nomad jobs are
          left running; stop them separately.
        </Confirm>
      )}
    </div>
  )
}
