import { useState } from 'react'
import { useProject } from '../../components/layout/context'
import { ErrorNote } from '../../components/ui/states'
import { api } from '../../lib/api'
import { App, parseEnv } from './serviceHelpers'

export function EditService({ a, onClose, onSaved }: { a: App; onClose: () => void; onSaved: () => void }) {
  const { project } = useProject()
  const [f, setF] = useState({
    image: a.image,
    ports: a.ports.join(', '),
    health_path: a.health_path,
    proxy: a.proxy,
    cpus: a.cpus ? String(a.cpus) : '',
    memory_mb: a.memory_mb ? String(a.memory_mb) : '',
    instances: String(a.instances),
    branch: a.branch,
    dockerfile: a.dockerfile,
    build_context: a.build_context,
    auto_deploy: a.auto_deploy,
    repo_token: '',
  })
  const [envText, setEnvText] = useState('')
  const [unset, setUnset] = useState<string[]>([])
  const [error, setError] = useState<string>()
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setF({ ...f, [k]: e.target.type === 'checkbox' ? e.target.checked : e.target.value })
  const save = async () => {
    const [env, envErr] = parseEnv(envText)
    if (envErr) return setError(envErr)
    try {
      await api('PATCH', `/projects/${project.id}/apps/${a.id}`, {
        ports: f.ports.split(/[\s,]+/).filter(Boolean),
        health_path: f.health_path,
        env,
        unset_env: unset,
        cpus: +f.cpus || 0,
        memory_mb: +f.memory_mb || 0,
        ...(a.repo_url
          ? {
              branch: f.branch,
              dockerfile: f.dockerfile,
              build_context: f.build_context,
              auto_deploy: f.auto_deploy,
              ...(f.repo_token ? { repo_token: f.repo_token } : {}),
            }
          : { image: f.image }),
        ...(a.orchestrator === 'nomad' ? { instances: +f.instances } : { proxy: f.proxy }),
      })
      onSaved()
      onClose()
    } catch (e: any) {
      setError(e.message)
    }
  }
  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="dialog wide" role="dialog" aria-modal aria-label="Edit service">
        <div className="body">
          <h2>Edit {a.name}</h2>
          {a.repo_url ? (
            <div className="grid-2">
              <label className="field">
                <span>Branch</span>
                <input className="input mono" value={f.branch} onChange={set('branch')} />
              </label>
              <label className="field">
                <span>Dockerfile</span>
                <input className="input mono" value={f.dockerfile} onChange={set('dockerfile')} />
              </label>
              <label className="field">
                <span>Build context</span>
                <input className="input mono" value={f.build_context} onChange={set('build_context')} />
              </label>
              <label className="field">
                <span>Replace access token</span>
                <input
                  className="input mono"
                  type="password"
                  value={f.repo_token}
                  onChange={set('repo_token')}
                  autoComplete="new-password"
                />
              </label>
              <label className="row">
                <input type="checkbox" checked={f.auto_deploy} onChange={set('auto_deploy')} /> Deploy on push
              </label>
            </div>
          ) : (
            <label className="field">
              <span>Image</span>
              <input className="input mono" value={f.image} onChange={set('image')} />
            </label>
          )}
          <div className="grid-2">
            <label className="field">
              <span>Ports</span>
              <input className="input mono" value={f.ports} onChange={set('ports')} />
            </label>
            <label className="field">
              <span>Health check path</span>
              <input className="input mono" value={f.health_path} onChange={set('health_path')} />
            </label>
            <label className="field">
              <span>CPU limit</span>
              <input className="input mono" value={f.cpus} onChange={set('cpus')} placeholder="unlimited" />
            </label>
            <label className="field">
              <span>Memory limit (MB)</span>
              <input className="input mono" value={f.memory_mb} onChange={set('memory_mb')} placeholder="unlimited" />
            </label>
            {a.orchestrator === 'nomad' && (
              <label className="field">
                <span>Instances</span>
                <input className="input mono" value={f.instances} onChange={set('instances')} />
              </label>
            )}
          </div>
          {a.orchestrator === 'docker' && (
            <label className="row">
              <input type="checkbox" checked={f.proxy} onChange={set('proxy')} /> Zero-downtime deploys (HTTP, via the
              agent proxy)
            </label>
          )}
          <div className="field">
            <span>Existing variables</span>
            <div className="row" style={{ flexWrap: 'wrap' }}>
              {a.env_keys.length === 0 && <span className="faint">None</span>}
              {a.env_keys.map((k) => (
                <button
                  key={k}
                  className={`btn sm ${unset.includes(k) ? 'danger' : ''}`}
                  onClick={() => setUnset(unset.includes(k) ? unset.filter((x) => x !== k) : [...unset, k])}
                >
                  <span className="mono" style={{ textDecoration: unset.includes(k) ? 'line-through' : undefined }}>
                    {k}
                  </span>{' '}
                  {unset.includes(k) ? '↺' : '×'}
                </button>
              ))}
            </div>
          </div>
          <label className="field">
            <span>Set variables</span>
            <textarea
              className="input"
              rows={4}
              placeholder="KEY=value (adds or overwrites)"
              value={envText}
              onChange={(e) => setEnvText(e.target.value)}
              spellCheck={false}
            />
          </label>
          <div className="faint" style={{ fontSize: 12 }}>
            Changes take effect on the next deploy.
          </div>
          <ErrorNote error={error} />
        </div>
        <div className="foot">
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" onClick={save}>
            Save
          </button>
        </div>
      </div>
    </div>
  )
}
