import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useProject } from '../../components/layout/context'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { ErrorNote } from '../../components/ui/states'
import { api, useApi } from '../../lib/api'
import { Server } from '../../lib/types'
import { NomadCluster } from '../nomad/nomadHelpers'
import { parseEnv } from './serviceHelpers'

const blank = {
  name: '',
  source: 'image' as 'image' | 'git',
  image: '',
  repo_url: '',
  branch: 'main',
  dockerfile: '',
  build_context: '',
  repo_token: '',
  auto_deploy: true,
  orchestrator: 'docker' as 'docker' | 'nomad',
  server_id: '',
  nomad_cluster_id: '',
  instances: '1',
  ports: '',
  health_path: '/health',
  proxy: false,
  cpus: '',
  memory_mb: '',
  env: '',
}

export function NewService() {
  const { project } = useProject()
  const nav = useNavigate()
  const [servers] = useApi<Server[]>(`/projects/${project.id}/servers`)
  const [clusters] = useApi<NomadCluster[]>(`/projects/${project.id}/nomad/clusters`)
  const [f, setF] = useState(blank)
  const [error, setError] = useState<string>()
  const set =
    (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) =>
      setF({ ...f, [k]: e.target.type === 'checkbox' ? (e.target as HTMLInputElement).checked : e.target.value })
  const ports = f.ports.split(/[\s,]+/).filter(Boolean)
  const [env, envErr] = parseEnv(f.env)
  const serverId = f.server_id || servers?.[0]?.id || ''
  const clusterId = f.nomad_cluster_id || clusters?.[0]?.id || ''
  const nomad = f.orchestrator === 'nomad'

  const yaml = [
    `name: ${f.name || '<name>'}`,
    f.source === 'git'
      ? `build:\n  repository: ${f.repo_url || '<repo>'}\n  branch: ${f.branch}${f.dockerfile ? `\n  dockerfile: ${f.dockerfile}` : ''}`
      : `image: ${f.image || '<image>'}`,
    nomad
      ? `nomad:\n  instances: ${f.instances}`
      : `server: ${servers?.find((s) => s.id === serverId)?.name ?? '<server>'}`,
    ...(ports.length ? ['ports:', ...ports.map((p) => `  - ${p}`)] : []),
    ...(Object.keys(env).length ? ['environment:', ...Object.keys(env).map((k) => `  ${k}: ••••••`)] : []),
    ...(f.cpus || f.memory_mb
      ? ['resources:', ...(f.cpus ? [`  cpu: ${f.cpus}`] : []), ...(f.memory_mb ? [`  memory: ${f.memory_mb}mb`] : [])]
      : []),
    ...(f.health_path ? ['healthcheck:', `  path: ${f.health_path}`] : []),
    ...(f.proxy && !nomad ? ['deploy:', '  strategy: zero-downtime'] : []),
  ].join('\n')

  const submit = async () => {
    if (envErr) return setError(envErr)
    try {
      const a = await api<{ id: string }>('POST', `/projects/${project.id}/apps`, {
        name: f.name,
        ports,
        health_path: f.health_path,
        env,
        orchestrator: f.orchestrator,
        ...(nomad ? { nomad_cluster_id: clusterId, instances: +f.instances } : { server_id: serverId, proxy: f.proxy }),
        ...(f.source === 'git'
          ? {
              repo_url: f.repo_url,
              branch: f.branch,
              dockerfile: f.dockerfile,
              build_context: f.build_context,
              auto_deploy: f.auto_deploy,
              ...(f.repo_token ? { repo_token: f.repo_token } : {}),
            }
          : { image: f.image }),
        cpus: +f.cpus || 0,
        memory_mb: +f.memory_mb || 0,
      })
      nav(`/p/${project.id}/services/${a.id}`)
    } catch (e: any) {
      setError(e.message)
    }
  }

  return (
    <div className="page">
      <PageHead crumbs={<Link to={`/p/${project.id}/services`}>Services</Link>} title="New service" />
      <div className="split">
        <Panel pad>
          <div className="form">
            <label className="field">
              <span>Name</span>
              <input className="input mono" placeholder="api" value={f.name} onChange={set('name')} autoFocus />
              <span className="hint">Lowercase letters, digits and dashes.</span>
            </label>
            <div className="field">
              <span>Source</span>
              <div className="seg" style={{ alignSelf: 'start' }}>
                {(['image', 'git'] as const).map((s) => (
                  <button key={s} className={f.source === s ? 'on' : ''} onClick={() => setF({ ...f, source: s })}>
                    {s === 'image' ? 'Container image' : 'Git repository'}
                  </button>
                ))}
              </div>
            </div>
            {f.source === 'image' ? (
              <label className="field">
                <span>Image</span>
                <input
                  className="input mono"
                  placeholder="ghcr.io/company/api:latest"
                  value={f.image}
                  onChange={set('image')}
                />
                <span className="hint">Public registries.</span>
              </label>
            ) : (
              <>
                <div className="grid-2">
                  <label className="field">
                    <span>Repository (https)</span>
                    <input
                      className="input mono"
                      placeholder="https://github.com/company/api"
                      value={f.repo_url}
                      onChange={set('repo_url')}
                    />
                  </label>
                  <label className="field">
                    <span>Branch</span>
                    <input className="input mono" value={f.branch} onChange={set('branch')} />
                  </label>
                  <label className="field">
                    <span>Dockerfile</span>
                    <input
                      className="input mono"
                      placeholder="Dockerfile"
                      value={f.dockerfile}
                      onChange={set('dockerfile')}
                    />
                  </label>
                  <label className="field">
                    <span>Build context</span>
                    <input
                      className="input mono"
                      placeholder="/"
                      value={f.build_context}
                      onChange={set('build_context')}
                    />
                  </label>
                </div>
                <label className="field">
                  <span>Access token (private repos)</span>
                  <input
                    className="input mono"
                    type="password"
                    value={f.repo_token}
                    onChange={set('repo_token')}
                    autoComplete="new-password"
                  />
                  <span className="hint">
                    A GitHub fine-grained token with read access to contents. Encrypted; never shown again.
                  </span>
                </label>
                <label className="row">
                  <input type="checkbox" checked={f.auto_deploy} onChange={set('auto_deploy')} /> Deploy automatically
                  on push (configure the webhook after creating)
                </label>
              </>
            )}
            <div className="field">
              <span>Run on</span>
              <div className="seg" style={{ alignSelf: 'start' }}>
                <button className={!nomad ? 'on' : ''} onClick={() => setF({ ...f, orchestrator: 'docker' })}>
                  Single server (Docker)
                </button>
                <button
                  className={nomad ? 'on' : ''}
                  disabled={!clusters?.length || f.source === 'git'}
                  title={
                    !clusters?.length
                      ? 'No Nomad cluster in this project'
                      : f.source === 'git'
                        ? 'Use a registry image for Nomad'
                        : ''
                  }
                  onClick={() => setF({ ...f, orchestrator: 'nomad' })}
                >
                  Nomad cluster
                </button>
              </div>
            </div>
            {nomad ? (
              <div className="grid-2">
                <label className="field">
                  <span>Cluster</span>
                  <select className="select" value={clusterId} onChange={set('nomad_cluster_id')}>
                    {clusters?.map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.region} / {c.datacenter} · {c.nodes.length} nodes
                      </option>
                    ))}
                  </select>
                </label>
                <label className="field">
                  <span>Instances</span>
                  <input className="input mono" value={f.instances} onChange={set('instances')} />
                  <span className="hint">One per server; rolling updates with auto-revert.</span>
                </label>
              </div>
            ) : (
              <label className="field">
                <span>Server</span>
                <select className="select" value={serverId} onChange={set('server_id')}>
                  {servers?.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                      {s.region ? ` · ${s.region}` : ''}
                      {s.status === 'offline' ? ' (offline)' : ''}
                    </option>
                  ))}
                </select>
              </label>
            )}
            <div className="grid-2">
              <label className="field">
                <span>Ports</span>
                <input className="input mono" placeholder="3000:3000" value={f.ports} onChange={set('ports')} />
                <span className="hint">host:container, comma separated.</span>
              </label>
              <label className="field">
                <span>Health check path</span>
                <input
                  className="input mono"
                  placeholder="/health"
                  value={f.health_path}
                  onChange={set('health_path')}
                />
                <span className="hint">GET must return &lt; 400.</span>
              </label>
              <label className="field">
                <span>CPU limit</span>
                <input
                  className="input mono"
                  placeholder={nomad ? '0.1' : 'unlimited'}
                  value={f.cpus}
                  onChange={set('cpus')}
                />
              </label>
              <label className="field">
                <span>Memory limit (MB)</span>
                <input
                  className="input mono"
                  placeholder={nomad ? '256' : 'unlimited'}
                  value={f.memory_mb}
                  onChange={set('memory_mb')}
                />
              </label>
            </div>
            {!nomad && (
              <label className="row" style={{ alignItems: 'flex-start' }}>
                <input type="checkbox" checked={f.proxy} onChange={set('proxy')} style={{ marginTop: 3 }} />
                <span>
                  Zero-downtime deploys (HTTP)
                  <div className="hint faint" style={{ fontSize: 12 }}>
                    The agent serves the first port and switches traffic to the new container once it's healthy. Also
                    records request rate, error rate and latency.
                  </div>
                </span>
              </label>
            )}
            <label className="field">
              <span>Environment</span>
              <textarea
                className="input"
                rows={5}
                placeholder={'DATABASE_URL=postgres://…\nREDIS_URL=redis://…'}
                value={f.env}
                onChange={set('env')}
                spellCheck={false}
              />
              <span className="hint">
                Encrypted at rest. Organization and project secrets are merged in; values here win.
              </span>
            </label>
            <ErrorNote error={error} />
            <div className="row">
              <div className="spacer" />
              <button
                className="btn primary"
                disabled={!f.name || (f.source === 'image' ? !f.image : !f.repo_url)}
                onClick={submit}
              >
                Create service
              </button>
            </div>
          </div>
        </Panel>
        <Panel title="Definition">
          <div className="panel-body">
            <pre className="code">{yaml}</pre>
          </div>
        </Panel>
      </div>
    </div>
  )
}
