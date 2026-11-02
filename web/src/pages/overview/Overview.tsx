import { useState } from 'react'
import { Link } from 'react-router'
import { Chart } from '../../components/charts/Chart'
import { MetricCharts } from '../../components/charts/MetricCharts'
import { useProject } from '../../components/layout/context'
import { RangePicker } from '../../components/ui/controls'
import { EventList } from '../../components/ui/EventList'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Dot, StatusText, Tone } from '../../components/ui/status'
import { useApi } from '../../lib/api'
import { bitrate, bytes, pct } from '../../lib/format'
import { useLive } from '../../lib/live'
import { Deployment, OrbitEvent, Series } from '../../lib/types'
import { DeploymentRows } from '../deployments/DeploymentRows'
import { Attention, AttentionKind } from './Attention'

export interface OverviewData {
  servers: { total: number; online: number; cpus: number; mem_total: number }
  containers: { total: number; running: number; unhealthy: number }
  apps: { total: number; healthy: number; proxied: number }
  databases: { total: number; healthy: number; replicas: number; replicas_healthy: number; unhealthy: number }
  usage: { cpu: number | null; mem: number | null; disk: number | null; net: number | null }
  deployments: Deployment[]
  events: OrbitEvent[]
  attention: { kind: AttentionKind; id: string; name: string; n: number }[]
  incidents: {
    id: number
    rule: string
    metric: string
    value: number
    threshold: number
    target_type: string
    target_id: string
    target_name: string
    server_id: string | null
    opened_at: string
  }[]
}

export function Overview() {
  const { project } = useProject()
  const [range, setRange] = useState('1h')
  const [data, , reload] = useApi<OverviewData>(`/projects/${project.id}/overview`)
  const [metrics, , reloadMetrics] = useApi<Series>(`/projects/${project.id}/metrics?range=${range}`)
  const [appM, , reloadApp] = useApi<Series>(`/projects/${project.id}/app-metrics?range=${range}`)
  const [dbM, , reloadDb] = useApi<Series>(`/projects/${project.id}/database-metrics?range=${range}`)
  useLive(project.id, ['servers', 'containers', 'deployments', 'events', 'alerts', 'databases', 'nomad'], reload)
  useLive(
    project.id,
    ['metrics'],
    () => {
      reloadMetrics()
      reloadApp()
    },
    10000,
  )
  useLive(project.id, ['databases'], reloadDb, 15000)

  if (!data)
    return (
      <div className="page">
        <PageHead title="Overview" />
      </div>
    )
  const { servers, containers, apps, usage, databases: dbs } = data
  const offline = servers.total - servers.online
  const appsDown = apps.total - apps.healthy
  const issues =
    offline + data.incidents.length + containers.unhealthy + appsDown + dbs.unhealthy + data.attention.length
  const health: [Tone, string] =
    servers.total === 0
      ? ['', 'No servers connected']
      : issues === 0
        ? ['ok', 'All systems healthy']
        : ['warn', issues === 1 ? '1 issue needs attention' : `${issues} issues need attention`]

  return (
    <div className="page">
      <PageHead title="Overview" sub={<StatusText tone={health[0]}>{health[1]}</StatusText>} />

      {servers.total === 0 && (
        <div className="panel" style={{ marginBottom: 16 }}>
          <div className="panel-body row" style={{ gap: 16 }}>
            <div style={{ flex: 1 }}>
              <div style={{ fontWeight: 500 }}>Connect your first server</div>
              <div className="muted">
                Install the agent with one command. It connects outbound over TLS; no inbound ports are needed.
              </div>
            </div>
            <Link className="btn primary" to={`/p/${project.id}/settings/agents`}>
              Add server
            </Link>
          </div>
        </div>
      )}

      <div className="cards">
        <Link to={`/p/${project.id}/servers`} className="card">
          <div className="k">Infrastructure</div>
          <div className="v">
            {servers.total}
            <small>servers</small>
          </div>
          <div className="s">
            <Dot tone={servers.total === 0 ? '' : offline ? 'err' : 'ok'} />
            {servers.total === 0 ? 'None connected' : offline ? `${offline} offline` : 'All online'}
          </div>
        </Link>
        <Link to={`/p/${project.id}/services`} className="card">
          <div className="k">Applications</div>
          <div className="v">
            {apps.total}
            <small>services</small>
          </div>
          <div className="s">
            <Dot tone={apps.total === 0 ? '' : appsDown ? 'warn' : 'ok'} />
            {apps.total === 0 ? 'None defined' : `${apps.healthy} healthy`}
          </div>
        </Link>
        <Link to={`/p/${project.id}/docker`} className="card">
          <div className="k">Containers</div>
          <div className="v">
            {containers.running}
            <small>/ {containers.total} running</small>
          </div>
          <div className="s">
            <Dot tone={containers.unhealthy ? 'err' : containers.total ? 'ok' : ''} />
            {containers.unhealthy ? `${containers.unhealthy} unhealthy` : 'No unhealthy containers'}
          </div>
        </Link>
        <Link to={`/p/${project.id}/databases`} className="card">
          <div className="k">Databases</div>
          <div className="v">
            {dbs.total}
            <small>{dbs.replicas ? `+ ${dbs.replicas} replicas` : 'databases'}</small>
          </div>
          <div className="s">
            <Dot tone={dbs.total + dbs.replicas === 0 ? '' : dbs.unhealthy ? 'warn' : 'ok'} />
            {dbs.total + dbs.replicas === 0
              ? 'None connected'
              : dbs.unhealthy
                ? `${dbs.unhealthy} need attention`
                : `${dbs.healthy} healthy${dbs.replicas ? ` · ${dbs.replicas_healthy}/${dbs.replicas} replicas healthy` : ''}`}
          </div>
        </Link>
      </div>

      {issues > 0 && <Attention data={data} />}

      <div className="section-title">
        Resources
        <span className="faint mono" style={{ fontWeight: 400 }}>
          now: cpu {pct(usage.cpu)} · mem {pct(usage.mem)} · disk {pct(usage.disk)} · net {bitrate(usage.net)} ·{' '}
          {servers.cpus} vCPU · {bytes(servers.mem_total, 0)} RAM
        </span>
        <div className="spacer" />
        <RangePicker value={range} onChange={setRange} />
      </div>
      <MetricCharts data={metrics} aggregate />
      <div className="charts" style={{ marginTop: 12 }}>
        {apps.proxied > 0 && appM ? (
          <>
            <Chart
              title="Request rate (zero-downtime services)"
              t={appM.t}
              series={[{ label: 'req/s', values: appM.rps, color: 'series-1' }]}
              format={(v) => `${v.toFixed(1)}/s`}
            />
            <Chart
              title="Error rate (5xx)"
              t={appM.t}
              series={[{ label: 'errors', values: appM.error_rate, color: 'series-1' }]}
              format={(v) => `${v.toFixed(2)}%`}
            />
          </>
        ) : null}
        {dbs.total + dbs.replicas > 0 && dbM ? (
          <>
            <Chart
              title="Database connections"
              t={dbM.t}
              series={[{ label: 'connections', values: dbM.connections, color: 'series-1' }]}
              format={(v) => v.toFixed(0)}
              integer
            />
            <Chart
              title="Replication lag (worst replica)"
              t={dbM.t}
              series={[{ label: 'lag', values: dbM.lag_ms, color: 'series-1' }]}
              format={(v) => (v < 1000 ? `${v.toFixed(0)}ms` : `${(v / 1000).toFixed(1)}s`)}
            />
          </>
        ) : null}
      </div>
      {(apps.proxied === 0 || dbs.total + dbs.replicas === 0) && (
        <div className="faint" style={{ fontSize: 12, marginTop: 8 }}>
          {apps.proxied === 0 && 'Request and error rates appear for services with zero-downtime deploys enabled. '}
          {dbs.total + dbs.replicas === 0 &&
            'Database connections and replication lag appear once a database is connected.'}
        </div>
      )}

      <div className="split" style={{ marginTop: 20 }}>
        <Panel
          title="Recent deployments"
          actions={
            <Link className="btn ghost sm" to={`/p/${project.id}/deployments`}>
              View all
            </Link>
          }
        >
          <DeploymentRows deployments={data.deployments} />
        </Panel>
        <Panel
          title="Events"
          actions={
            <Link className="btn ghost sm" to={`/p/${project.id}/events`}>
              View all
            </Link>
          }
        >
          <EventList events={data.events} />
        </Panel>
      </div>
    </div>
  )
}
