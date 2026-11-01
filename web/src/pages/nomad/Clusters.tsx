import { useState } from 'react'
import { Link } from 'react-router'
import { useCan, useProject } from '../../components/layout/context'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { useApi } from '../../lib/api'
import { ago } from '../../lib/format'
import { Server } from '../../lib/types'
import { CreateCluster } from './CreateClusterDialog'
import { serverFor, statusTone, useClusters } from './nomadHelpers'

export function Clusters() {
  const { project } = useProject()
  const canAdmin = useCan('admin')
  const [cs] = useClusters()
  const [servers] = useApi<Server[]>(`/projects/${project.id}/servers`)
  const [creating, setCreating] = useState(false)
  return (
    <div className="page">
      <PageHead
        title="Clusters"
        sub="Nomad clusters discovered by your agents. Nomad is optional; single-server Docker works without it."
      >
        {canAdmin && (
          <button className="btn primary" onClick={() => setCreating(true)}>
            Create Nomad cluster
          </button>
        )}
      </PageHead>
      {!cs ? (
        <Loading />
      ) : !cs.length ? (
        <Panel>
          <Empty title="No Nomad clusters">
            Agents report any Nomad agent reachable at 127.0.0.1:4646 (or NOMAD_ADDR). You can also create a cluster
            from your servers.
          </Empty>
        </Panel>
      ) : (
        <div className="stack">
          {cs.map((c) => {
            const cpu = c.nodes.reduce((a, n) => a + (n.NodeResources?.Cpu?.CpuShares ?? 0), 0)
            const mem = c.nodes.reduce((a, n) => a + (n.NodeResources?.Memory?.MemoryMB ?? 0), 0)
            const running = c.allocations.filter((a) => a.ClientStatus === 'running')
            return (
              <Panel
                key={c.id}
                title={
                  <>
                    {c.region} <span className="faint">/ {c.datacenter}</span>
                  </>
                }
                actions={
                  <span className="row" style={{ gap: 8 }}>
                    <StatusText tone={c.fresh ? 'ok' : 'warn'}>
                      {c.fresh ? 'reporting' : `stale · ${ago(c.updated_at)}`}
                    </StatusText>
                    <span className="faint mono" style={{ fontSize: 12 }}>
                      leader {c.leader || '—'}
                    </span>
                  </span>
                }
              >
                <div className="panel-body row" style={{ gap: 28 }}>
                  <div>
                    <div className="faint" style={{ fontSize: 12 }}>
                      Nodes
                    </div>
                    <div className="mono">
                      {c.nodes.filter((n) => n.Status === 'ready').length} / {c.nodes.length} ready
                    </div>
                  </div>
                  <div>
                    <div className="faint" style={{ fontSize: 12 }}>
                      Jobs
                    </div>
                    <div className="mono">{c.jobs.length}</div>
                  </div>
                  <div>
                    <div className="faint" style={{ fontSize: 12 }}>
                      Running allocations
                    </div>
                    <div className="mono">{running.length}</div>
                  </div>
                  <div>
                    <div className="faint" style={{ fontSize: 12 }}>
                      CPU capacity
                    </div>
                    <div className="mono">{cpu.toLocaleString()} MHz</div>
                  </div>
                  <div>
                    <div className="faint" style={{ fontSize: 12 }}>
                      Memory capacity
                    </div>
                    <div className="mono">{mem.toLocaleString()} MB</div>
                  </div>
                </div>
                <table className="t" style={{ borderTop: '1px solid var(--border)' }}>
                  <thead>
                    <tr>
                      <th>Node</th>
                      <th>Status</th>
                      <th>Orbit server</th>
                      <th>Address</th>
                      <th>Datacenter</th>
                      <th>Eligibility</th>
                      <th className="right">Allocations</th>
                    </tr>
                  </thead>
                  <tbody>
                    {c.nodes.map((n) => {
                      const s = serverFor(n, c, servers)
                      return (
                        <tr key={n.ID}>
                          <td className="name">{n.Name}</td>
                          <td>
                            <StatusText tone={statusTone(n.Status)}>
                              {n.Status}
                              {n.Drain ? ' · draining' : ''}
                            </StatusText>
                          </td>
                          <td>
                            {s ? (
                              <Link to={`/p/${project.id}/servers/${s.id}/overview`}>{s.name}</Link>
                            ) : (
                              <span className="faint">not enrolled</span>
                            )}
                          </td>
                          <td className="mono muted">{n.Address}</td>
                          <td className="muted">{n.Datacenter}</td>
                          <td className="muted">{n.SchedulingEligibility}</td>
                          <td className="right mono">
                            {c.allocations.filter((a) => a.NodeID === n.ID && a.ClientStatus === 'running').length}
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </Panel>
            )
          })}
        </div>
      )}
      {creating && <CreateCluster onClose={() => setCreating(false)} />}
    </div>
  )
}
