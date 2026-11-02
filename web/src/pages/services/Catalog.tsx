import { useState } from 'react'
import { Link } from 'react-router'
import { useCan, useProject } from '../../components/layout/context'
import { Copy } from '../../components/ui/controls'
import { Dialog } from '../../components/ui/Dialog'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { ErrorNote, Loading } from '../../components/ui/states'
import { api, useApi } from '../../lib/api'
import { ServerSelect } from '../databases/ServerSelect'

type CatalogApp = {
  id: string
  name: string
  category: string
  description: string
  website: string
  image?: string
  ports: { container: string; host: number; label: string }[]
  volumes: string[]
  inputs: { key: string; label: string; type: string; default?: string; required?: boolean; hint?: string }[]
  notes?: string
  companions: { suffix: string; label: string; optional?: boolean }[]
  link?: string
}

type Installed = {
  services: { id: string; name: string; deployment_id?: string; waiting?: boolean; error?: string }[]
  credentials: { service: string; label: string; value: string }[]
}

export function Catalog() {
  const { project } = useProject()
  const canEdit = useCan('member')
  const [catalog] = useApi<{ categories: { id: string; name: string }[]; apps: CatalogApp[] }>(
    `/projects/${project.id}/catalog`,
  )
  const [installing, setInstalling] = useState<CatalogApp>()
  return (
    <div className="page">
      <PageHead title="One-click apps" sub="Ready-made services, with their databases and secrets wired up" />
      {!catalog ? (
        <Loading />
      ) : (
        catalog.categories.map((c) => (
          <div key={c.id} style={{ marginBottom: 16 }}>
            <Panel title={c.name} pad>
              <div className="cards">
                {catalog.apps
                  .filter((a) => a.category === c.id)
                  .map((a) =>
                    a.link ? (
                      <Link key={a.id} className="wf-node" to={`/p/${project.id}/${a.link}`}>
                        <b>{a.name}</b>
                        <span className="muted">{a.description}</span>
                      </Link>
                    ) : (
                      <button key={a.id} className="wf-node" disabled={!canEdit} onClick={() => setInstalling(a)}>
                        <b>{a.name}</b>
                        <span className="muted">{a.description}</span>
                      </button>
                    ),
                  )}
              </div>
            </Panel>
          </div>
        ))
      )}
      {installing && <InstallDialog app={installing} onClose={() => setInstalling(undefined)} />}
    </div>
  )
}

function InstallDialog({ app, onClose }: { app: CatalogApp; onClose: () => void }) {
  const { project } = useProject()
  const [name, setName] = useState(app.id)
  const [serverId, setServerId] = useState('')
  const [ports, setPorts] = useState(app.ports.map((p) => String(p.host)))
  const [inputs, setInputs] = useState<Record<string, string>>({})
  const [companions, setCompanions] = useState<Record<string, boolean>>({})
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState<Installed>()
  const install = () => {
    setBusy(true)
    api<Installed>('POST', `/projects/${project.id}/catalog/install`, {
      app: app.id,
      server_id: serverId,
      name,
      ports: ports.map((p) => +p || 0),
      // Omitted inputs get their defaults server-side.
      inputs: Object.fromEntries(Object.entries(inputs).filter(([, v]) => v !== '')),
      companions,
    }).then(setDone, (e) => {
      setError(e.message)
      setBusy(false)
    })
  }
  if (done)
    return (
      <Dialog
        title={`${app.name} installed`}
        onClose={onClose}
        footer={
          <button className="btn primary" onClick={onClose}>
            Done
          </button>
        }
      >
        <div className="list">
          {done.services.map((s) => (
            <div key={s.id} className="list-item">
              <Link className="name" to={`/p/${project.id}/services/${s.id}`}>
                {s.name}
              </Link>{' '}
              <span className="muted">
                {s.error ?? (s.waiting ? 'deploys once its companions are healthy' : 'deploying')}
              </span>
            </div>
          ))}
        </div>
        {done.credentials.length > 0 && (
          <>
            <div className="callout warn">Save these credentials now; they aren't shown again.</div>
            <div className="list">
              {done.credentials.map((c) => (
                <div key={c.service + c.label} className="list-item row">
                  <span style={{ flex: 1 }}>
                    {c.label} <span className="muted">({c.service})</span>
                    <div className="mono">{c.value}</div>
                  </span>
                  <Copy text={c.value} />
                </div>
              ))}
            </div>
          </>
        )}
        {app.notes && <div className="muted">{app.notes}</div>}
      </Dialog>
    )
  return (
    <Dialog
      title={`Install ${app.name}`}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" onClick={install} disabled={busy || !serverId}>
            Install
          </button>
        </>
      }
    >
      <div className="muted">
        {app.description}{' '}
        {app.image && (
          <>
            Runs <span className="mono">{app.image}</span>.
          </>
        )}
      </div>
      <div className="grid-2">
        <label className="field">
          <span>Name</span>
          <input className="input mono" value={name} onChange={(e) => setName(e.target.value)} autoFocus />
        </label>
        <label className="field">
          <span>Server</span>
          <ServerSelect value={serverId} onChange={setServerId} />
        </label>
        {app.ports.map((p, i) => (
          <label key={p.container} className="field">
            <span>
              {p.label} host port <span className="mono">→ {p.container}</span>
            </span>
            <input
              className="input mono"
              placeholder="not published"
              value={ports[i]}
              onChange={(e) => setPorts(ports.map((v, j) => (j === i ? e.target.value : v)))}
            />
          </label>
        ))}
        {app.inputs.map((i) =>
          i.type === 'checkbox' ? (
            <label key={i.key} className="row">
              <input
                type="checkbox"
                checked={(inputs[i.key] ?? i.default) === 'true'}
                onChange={(e) => setInputs({ ...inputs, [i.key]: String(e.target.checked) })}
              />{' '}
              {i.label}
            </label>
          ) : (
            <label key={i.key} className="field">
              <span>
                {i.label}
                {i.required ? '' : ' (optional)'}
              </span>
              <input
                className="input"
                type={i.type}
                placeholder={i.default}
                value={inputs[i.key] ?? ''}
                onChange={(e) => setInputs({ ...inputs, [i.key]: e.target.value })}
              />
              {i.hint && <span className="hint">{i.hint}</span>}
            </label>
          ),
        )}
      </div>
      {app.companions.map((c) => (
        <label key={c.suffix} className="row">
          <input
            type="checkbox"
            checked={!c.optional || !!companions[c.suffix]}
            disabled={!c.optional}
            onChange={(e) => setCompanions({ ...companions, [c.suffix]: e.target.checked })}
          />{' '}
          {c.label}{' '}
          <span className="muted mono">
            ({name}-{c.suffix})
          </span>
        </label>
      ))}
      <ErrorNote error={error} />
    </Dialog>
  )
}
