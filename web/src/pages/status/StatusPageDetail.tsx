import { useState } from 'react'
import { Link, NavLink, useNavigate, useParams } from 'react-router'
import { useCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { Dialog } from '../../components/ui/Dialog'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Copy } from '../../components/ui/controls'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { Badge, StatusText } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { ago, datetime } from '../../lib/format'
import { useLive } from '../../lib/live'
import { Server } from '../../lib/types'
import { Monitor } from '../monitors/Monitors'
import { App } from '../services/serviceHelpers'
import { NewIncidentDialog, UpdateDialog } from './IncidentDialogs'
import {
  COMPONENT_STATUSES,
  Component,
  Incident,
  StatusPage,
  componentLabel,
  componentTone,
  impactTone,
  statusLabel,
} from './statusHelpers'

const TABS = ['incidents', 'components', 'subscribers', 'settings'] as const

export function StatusPageDetail() {
  const { project } = useProject()
  const { id, tab = 'incidents' } = useParams()
  const [page, error, reload] = useApi<StatusPage>(`/projects/${project.id}/status-pages/${id}`)
  useLive(project.id, ['monitors', 'servers', 'containers'], reload, 5000)
  if (error)
    return (
      <div className="page">
        <ErrorNote error={error} />
      </div>
    )
  if (!page) return <Loading />
  const base = `/p/${project.id}/status-pages/${page.id}`
  const worst = page.components.reduce(
    (w, c) =>
      COMPONENT_STATUSES.findIndex((s) => s[0] === c.status) > COMPONENT_STATUSES.findIndex((s) => s[0] === w)
        ? c.status
        : w,
    'operational' as string,
  )
  const href = page.domain ? `https://${page.domain}` : page.url
  return (
    <div className="page">
      <PageHead
        crumbs={<Link to={`/p/${project.id}/status-pages`}>Status pages</Link>}
        title={page.name}
        sub={
          <span className="row" style={{ gap: 10 }}>
            <StatusText tone={componentTone(worst)}>{componentLabel(worst)}</StatusText>
            <span className="faint">·</span>
            <a className="mono" href={href} target="_blank" rel="noreferrer">
              {href.replace(/^https?:\/\//, '')} ↗
            </a>
            {!page.public && <Badge>private</Badge>}
          </span>
        }
      />
      <div className="tabs">
        {TABS.map((t) => (
          <NavLink key={t} to={`${base}/${t}`} className={t === tab ? 'active' : ''}>
            {t[0].toUpperCase() + t.slice(1)}
          </NavLink>
        ))}
      </div>
      {tab === 'incidents' && <IncidentsTab page={page} reload={reload} />}
      {tab === 'components' && <ComponentsTab page={page} reload={reload} />}
      {tab === 'subscribers' && <SubscribersTab page={page} />}
      {tab === 'settings' && <SettingsTab page={page} reload={reload} />}
    </div>
  )
}

function IncidentsTab({ page, reload }: { page: StatusPage; reload: () => void }) {
  const canEdit = useCan('member')
  const [creating, setCreating] = useState<'incident' | 'maintenance' | null>(null)
  const [updating, setUpdating] = useState<Incident | null>(null)
  const open = page.incidents.filter((i) => !i.resolved_at)
  const past = page.incidents.filter((i) => i.resolved_at)
  const card = (i: Incident) => (
    <div key={i.id} className="list-item" style={{ alignItems: 'flex-start' }}>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div className="row" style={{ gap: 8 }}>
          <span style={{ fontWeight: 500 }}>{i.name}</span>
          <StatusText tone={i.resolved_at ? 'ok' : impactTone(i.impact)}>{statusLabel(i.status)}</StatusText>
          {i.kind === 'maintenance' ? <Badge tone="info">maintenance</Badge> : <Badge>{i.impact} impact</Badge>}
        </div>
        {i.kind === 'maintenance' && i.scheduled_for && (
          <div className="muted" style={{ fontSize: 13 }}>
            {datetime(i.scheduled_for)} – {datetime(i.scheduled_until!)}
            {i.auto_transition ? ' · starts and completes automatically' : ''}
          </div>
        )}
        {i.updates.slice(0, open.includes(i) ? 3 : 1).map((u) => (
          <div key={u.id} style={{ marginTop: 6 }}>
            <b style={{ fontWeight: 500 }}>{statusLabel(u.status)}</b> – {u.message}
            <div className="faint" style={{ fontSize: 12 }}>
              {ago(u.created_at)}
              {u.author && ` · ${u.author}`}
            </div>
          </div>
        ))}
      </div>
      {canEdit && !i.resolved_at && (
        <button className="btn sm" onClick={() => setUpdating(i)}>
          Post update
        </button>
      )}
    </div>
  )
  return (
    <div className="stack">
      <Panel
        title="Open incidents and maintenance"
        actions={
          canEdit && (
            <>
              <button className="btn sm" onClick={() => setCreating('maintenance')}>
                Schedule maintenance
              </button>
              <button className="btn sm primary" onClick={() => setCreating('incident')}>
                Report incident
              </button>
            </>
          )
        }
      >
        {!open.length ? (
          <Empty title="Nothing open">
            The page shows "All Systems Operational" unless a component reports a problem.
          </Empty>
        ) : (
          <div className="list">{open.map(card)}</div>
        )}
      </Panel>
      <Panel title="History">
        {!past.length ? <Empty title="No resolved incidents yet" /> : <div className="list">{past.map(card)}</div>}
      </Panel>
      {creating && <NewIncidentDialog page={page} kind={creating} onClose={() => setCreating(null)} onDone={reload} />}
      {updating && <UpdateDialog page={page} incident={updating} onClose={() => setUpdating(null)} onDone={reload} />}
    </div>
  )
}

const SOURCES = [
  ['manual', 'Set by hand'],
  ['monitor', 'An uptime monitor'],
  ['server', 'A server'],
  ['app', 'A service'],
] as const

function ComponentsTab({ page, reload }: { page: StatusPage; reload: () => void }) {
  const { project } = useProject()
  const canAdmin = useCan('admin')
  const [editing, setEditing] = useState<Component | 'new' | null>(null)
  const [monitors] = useApi<Monitor[]>(`/projects/${project.id}/monitors`)
  const [servers] = useApi<Server[]>(`/projects/${project.id}/servers`)
  const [apps] = useApi<App[]>(`/projects/${project.id}/apps`)
  const sourceName = (c: Component) => {
    const list: { id: string; name: string }[] | undefined =
      c.source_type === 'monitor'
        ? monitors
        : c.source_type === 'server'
          ? servers
          : c.source_type === 'app'
            ? apps
            : undefined
    return c.source_type === 'manual'
      ? 'Set by hand'
      : `${c.source_type}: ${list?.find((x) => x.id === c.source_id)?.name ?? '…'}`
  }
  const move = (i: number, dir: -1 | 1) => {
    const a = page.components[i]
    const b = page.components[i + dir]
    const path = `/projects/${project.id}/status-pages/${page.id}/components/`
    Promise.all([
      api('PATCH', path + a.id, { position: b.position }),
      api('PATCH', path + b.id, { position: a.position === b.position ? a.position + dir : a.position }),
    ]).then(reload)
  }
  return (
    <Panel
      title="Components"
      actions={
        canAdmin && (
          <button className="btn sm primary" onClick={() => setEditing('new')}>
            Add component
          </button>
        )
      }
    >
      {!page.components.length ? (
        <Empty title="No components yet">
          Components are the parts of your service visitors care about, such as "Website", "API" or "Email". Link one to
          a monitor, server or service and its status updates by itself.
        </Empty>
      ) : (
        <table className="t">
          <thead>
            <tr>
              <th>Component</th>
              <th>Status from</th>
              <th>Status</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {page.components.map((c, i) => (
              <tr key={c.id}>
                <td>
                  <div className="name">{c.name}</div>
                  <div className="sub">{c.group_name ? `Group: ${c.group_name}` : c.description || '—'}</div>
                </td>
                <td className="muted">{sourceName(c)}</td>
                <td>
                  <StatusText tone={componentTone(c.status)}>{componentLabel(c.status)}</StatusText>
                </td>
                <td className="right" style={{ whiteSpace: 'nowrap' }}>
                  {canAdmin && (
                    <>
                      <button
                        className="btn ghost sm"
                        disabled={i === 0}
                        onClick={() => move(i, -1)}
                        aria-label="Move up"
                      >
                        ↑
                      </button>
                      <button
                        className="btn ghost sm"
                        disabled={i === page.components.length - 1}
                        onClick={() => move(i, 1)}
                        aria-label="Move down"
                      >
                        ↓
                      </button>
                      <button className="btn ghost sm" onClick={() => setEditing(c)}>
                        Edit
                      </button>
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {editing && (
        <ComponentDialog
          page={page}
          component={editing === 'new' ? undefined : editing}
          options={{ monitor: monitors ?? [], server: servers ?? [], app: apps ?? [] }}
          onClose={() => setEditing(null)}
          onDone={reload}
        />
      )}
    </Panel>
  )
}

function ComponentDialog({
  page,
  component,
  options,
  onClose,
  onDone,
}: {
  page: StatusPage
  component?: Component
  options: Record<'monitor' | 'server' | 'app', { id: string; name: string }[]>
  onClose: () => void
  onDone: () => void
}) {
  const { project } = useProject()
  const [f, setF] = useState({
    name: component?.name ?? '',
    description: component?.description ?? '',
    group_name: component?.group_name ?? '',
    source_type: component?.source_type ?? 'manual',
    source_id: component?.source_id ?? '',
    manual_status: component?.manual_status ?? 'operational',
  })
  const [error, setError] = useState<string>()
  const [deleting, setDeleting] = useState(false)
  const groups = [...new Set(page.components.map((c) => c.group_name).filter(Boolean))]
  const path = `/projects/${project.id}/status-pages/${page.id}/components`
  const done = () => {
    onDone()
    onClose()
  }
  if (deleting && component)
    return (
      <Confirm
        title={`Delete ${component.name}?`}
        action="Delete component"
        danger
        onClose={() => setDeleting(false)}
        onConfirm={() => api('DELETE', `${path}/${component.id}`).then(done)}
      >
        Its uptime history is deleted, and it's removed from past incidents.
      </Confirm>
    )
  return (
    <Dialog
      title={component ? `Edit ${component.name}` : 'Add component'}
      onClose={onClose}
      footer={
        <>
          {component && (
            <button className="btn danger" style={{ marginRight: 'auto' }} onClick={() => setDeleting(true)}>
              Delete
            </button>
          )}
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            disabled={!f.name || (f.source_type !== 'manual' && !f.source_id)}
            onClick={() =>
              api(component ? 'PATCH' : 'POST', component ? `${path}/${component.id}` : path, f).then(done, (e) =>
                setError(e.message),
              )
            }
          >
            Save
          </button>
        </>
      }
    >
      <label className="field">
        <span>Name</span>
        <input className="input" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} autoFocus />
      </label>
      <label className="field">
        <span>Description (optional)</span>
        <input
          className="input"
          value={f.description}
          onChange={(e) => setF({ ...f, description: e.target.value })}
          placeholder="Shown when visitors hover the ? next to its name"
        />
      </label>
      <label className="field">
        <span>Group (optional)</span>
        <input
          className="input"
          list="component-groups"
          value={f.group_name}
          onChange={(e) => setF({ ...f, group_name: e.target.value })}
          placeholder="e.g. Data"
        />
        <datalist id="component-groups">
          {groups.map((g) => (
            <option key={g} value={g} />
          ))}
        </datalist>
      </label>
      <div className="grid-2">
        <label className="field">
          <span>Status comes from</span>
          <select
            className="select"
            value={f.source_type}
            onChange={(e) => setF({ ...f, source_type: e.target.value as Component['source_type'], source_id: '' })}
          >
            {SOURCES.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </label>
        {f.source_type === 'manual' ? (
          <label className="field">
            <span>Status</span>
            <select
              className="select"
              value={f.manual_status}
              onChange={(e) => setF({ ...f, manual_status: e.target.value as Component['manual_status'] })}
            >
              {COMPONENT_STATUSES.map(([v, l]) => (
                <option key={v} value={v}>
                  {l}
                </option>
              ))}
            </select>
          </label>
        ) : (
          <label className="field">
            <span>{SOURCES.find((s) => s[0] === f.source_type)![1].replace(/^An? /, '')}</span>
            <select className="select" value={f.source_id} onChange={(e) => setF({ ...f, source_id: e.target.value })}>
              <option value="">Choose…</option>
              {options[f.source_type].map((o) => (
                <option key={o.id} value={o.id}>
                  {o.name}
                </option>
              ))}
            </select>
          </label>
        )}
      </div>
      {f.source_type !== 'manual' && (
        <span className="hint">
          {f.source_type === 'monitor' && 'Down → major outage.'}
          {f.source_type === 'server' && 'Offline → major outage.'}
          {f.source_type === 'app' && 'Stopped → major outage, unhealthy → partial outage, restarting → degraded.'} Open
          incidents can raise its status further; maintenance shows "Under maintenance".
        </span>
      )}
      <ErrorNote error={error} />
    </Dialog>
  )
}

function SubscribersTab({ page }: { page: StatusPage }) {
  const { project } = useProject()
  const canAdmin = useCan('admin')
  const [subs, error, reload] = useApi<
    { id: string; email: string | null; webhook_url: string | null; confirmed_at: string | null; created_at: string }[]
  >(canAdmin ? `/projects/${project.id}/status-pages/${page.id}/subscribers` : null)
  if (!canAdmin)
    return (
      <Panel title="Subscribers">
        <div className="panel-body muted">
          {page.subscribers} confirmed subscribers. Seeing the list requires the admin role.
        </div>
      </Panel>
    )
  return (
    <Panel title={`Subscribers · ${page.subscribers} confirmed`}>
      <ErrorNote error={error} />
      {!subs ? (
        <Loading />
      ) : !subs.length ? (
        <Empty title="No subscribers yet">Visitors subscribe with "Subscribe to updates" on the page.</Empty>
      ) : (
        <table className="t">
          <thead>
            <tr>
              <th>Subscriber</th>
              <th>Type</th>
              <th>Since</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {subs.map((s) => (
              <tr key={s.id}>
                <td className="mono">{s.email ?? s.webhook_url}</td>
                <td>
                  {s.email ? 'Email' : 'Webhook'} {!s.confirmed_at && <Badge tone="warn">unconfirmed</Badge>}
                </td>
                <td className="muted">{ago(s.created_at)}</td>
                <td className="right">
                  <button
                    className="btn ghost sm danger"
                    onClick={() =>
                      api('DELETE', `/projects/${project.id}/status-pages/${page.id}/subscribers/${s.id}`).then(reload)
                    }
                  >
                    Remove
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Panel>
  )
}

function SettingsTab({ page, reload }: { page: StatusPage; reload: () => void }) {
  const { project } = useProject()
  const nav = useNavigate()
  const canAdmin = useCan('admin')
  const [f, setF] = useState({
    name: page.name,
    slug: page.slug,
    domain: page.domain ?? '',
    public: page.public,
    description: page.description,
    support_url: page.support_url,
    brand_color: page.brand_color,
  })
  const [msg, setMsg] = useState<string>()
  const [error, setError] = useState<string>()
  const [deleting, setDeleting] = useState(false)
  const path = `/projects/${project.id}/status-pages/${page.id}`
  const save = () => {
    setError(undefined)
    api('PATCH', path, f).then(
      () => {
        setMsg('Saved')
        reload()
      },
      (e) => setError(e.message),
    )
  }
  const uploadLogo = async (file: File) => {
    setError(undefined)
    const res = await fetch('/api/v1' + path + '/logo', {
      method: 'PUT',
      body: file,
      headers: { 'X-Orbit-Request': '1' },
    })
    if (!res.ok) setError((await res.json().catch(() => ({}))).error ?? 'Upload failed')
    else reload()
  }
  const appHost = location.host
  const pageURL = page.domain ? `https://${page.domain}` : page.url
  return (
    <div className="stack">
      <Panel title="Page">
        <div className="panel-body form" style={{ maxWidth: 560 }}>
          <div className="grid-2">
            <label className="field">
              <span>Name</span>
              <input
                className="input"
                disabled={!canAdmin}
                value={f.name}
                onChange={(e) => setF({ ...f, name: e.target.value })}
              />
            </label>
            <label className="field">
              <span>Address</span>
              <input
                className="input mono"
                disabled={!canAdmin}
                value={f.slug}
                onChange={(e) => setF({ ...f, slug: e.target.value })}
              />
              <span className="hint mono">
                {appHost}/status/{f.slug}
              </span>
            </label>
          </div>
          <label className="field">
            <span>Description (optional)</span>
            <input
              className="input"
              disabled={!canAdmin}
              value={f.description}
              onChange={(e) => setF({ ...f, description: e.target.value })}
              placeholder="Shown under the header"
            />
          </label>
          <div className="grid-2">
            <label className="field">
              <span>Support link (optional)</span>
              <input
                className="input"
                disabled={!canAdmin}
                value={f.support_url}
                onChange={(e) => setF({ ...f, support_url: e.target.value })}
                placeholder="https://example.com/support"
              />
            </label>
            <label className="field">
              <span>Brand color</span>
              <div className="row" style={{ gap: 8 }}>
                <input
                  type="color"
                  disabled={!canAdmin}
                  value={f.brand_color}
                  onChange={(e) => setF({ ...f, brand_color: e.target.value })}
                  aria-label="Brand color"
                />
                <span className="mono muted">{f.brand_color}</span>
              </div>
            </label>
          </div>
          <div className="checkbox-row">
            <label>
              <input
                type="checkbox"
                disabled={!canAdmin}
                checked={!f.public}
                onChange={(e) => setF({ ...f, public: !e.target.checked })}
              />
              Private: only signed-in members of this organization can see it
            </label>
          </div>
          {canAdmin && (
            <div className="row">
              <button className="btn primary" onClick={save}>
                Save
              </button>
              {msg && <span className="faint">{msg}</span>}
            </div>
          )}
        </div>
      </Panel>
      <Panel title="Logo">
        <div className="panel-body row" style={{ gap: 16 }}>
          {page.has_logo ? (
            <img
              src={`${page.url.replace(location.origin, '')}/logo?${Date.now()}`}
              alt=""
              style={{ maxHeight: 48, maxWidth: 240 }}
            />
          ) : (
            <span className="muted">No logo. The page shows its name instead.</span>
          )}
          {canAdmin && (
            <>
              <label className="btn sm">
                Upload PNG, JPEG or WebP
                <input
                  type="file"
                  accept="image/png,image/jpeg,image/webp,image/gif"
                  hidden
                  onChange={(e) => e.target.files?.[0] && uploadLogo(e.target.files[0])}
                />
              </label>
              {page.has_logo && (
                <button className="btn ghost sm danger" onClick={() => api('DELETE', path + '/logo').then(reload)}>
                  Remove
                </button>
              )}
            </>
          )}
        </div>
      </Panel>
      <Panel title="Custom domain">
        <div className="panel-body form" style={{ maxWidth: 560 }}>
          <label className="field">
            <span>Domain</span>
            <input
              className="input mono"
              disabled={!canAdmin}
              value={f.domain}
              onChange={(e) => setF({ ...f, domain: e.target.value })}
              placeholder="status.example.com"
            />
          </label>
          <span className="hint">
            Point a CNAME record for the domain at <span className="mono">{location.hostname}</span>, then save. With
            the bundled Caddy proxy (the https profile), a certificate is issued automatically on the first visit.
            Private pages work only at Orbit's own address.
          </span>
          {canAdmin && (
            <div>
              <button className="btn" onClick={save}>
                Save domain
              </button>
            </div>
          )}
        </div>
      </Panel>
      <Panel title="Share and embed">
        <div className="panel-body stack">
          {[
            ['Page', pageURL],
            ['Status badge (Markdown)', `[![Status](${pageURL}/badge.svg)](${pageURL})`],
            ['JSON API (Statuspage v2 format)', `${pageURL}/api/v2/summary.json`],
            ['Atom feed', `${pageURL}/history.atom`],
          ].map(([k, v]) => (
            <div key={k} className="field">
              <span>{k}</span>
              <div className="copy-row">
                <pre className="code">{v}</pre>
                <Copy text={v} />
              </div>
            </div>
          ))}
        </div>
      </Panel>
      <ErrorNote error={error} />
      {canAdmin && (
        <Panel title="Danger zone">
          <div className="panel-body row">
            <div style={{ flex: 1 }}>
              <div style={{ fontWeight: 500 }}>Delete status page</div>
              <div className="muted">Deletes its components, uptime history, incidents and subscribers.</div>
            </div>
            <button className="btn danger" onClick={() => setDeleting(true)}>
              Delete
            </button>
          </div>
        </Panel>
      )}
      {deleting && (
        <Confirm
          title={`Delete ${page.name}?`}
          action="Delete status page"
          danger
          typeToConfirm={page.slug}
          onClose={() => setDeleting(false)}
          onConfirm={() => api('DELETE', path).then(() => nav(`/p/${project.id}/status-pages`))}
        >
          This can't be undone. Visitors will get a "not found" page.
        </Confirm>
      )}
    </div>
  )
}
