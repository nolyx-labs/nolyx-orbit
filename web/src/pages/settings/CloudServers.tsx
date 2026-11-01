import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { Dialog } from '../../components/ui/Dialog'
import { Panel } from '../../components/ui/Panel'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { useLive } from '../../lib/live'

export function CloudServers() {
  const { project, org } = useProject()
  const [accounts] = useApi<{ id: string; name: string }[]>(`/orgs/${org.id}/cloud-accounts`)
  const [provisions, , reload] = useApi<any[]>(`/projects/${project.id}/provisions`)
  const [creating, setCreating] = useState(false)
  const [destroying, setDestroying] = useState<any>(null)
  useLive(project.id, ['servers', 'events'], reload, 5000)
  return (
    <Panel
      title="Create a server in your cloud"
      actions={
        accounts?.length ? (
          <button className="btn sm primary" onClick={() => setCreating(true)}>
            Create server
          </button>
        ) : null
      }
    >
      {!accounts ? (
        <Loading />
      ) : !accounts.length ? (
        <div className="panel-body muted">
          Connect a Hetzner Cloud account in{' '}
          <Link to={`/p/${project.id}/settings/organization`} style={{ color: 'var(--text)' }}>
            Organization settings
          </Link>{' '}
          and Orbit can create servers with Docker and the agent pre-installed.
        </div>
      ) : !provisions?.length ? (
        <Empty title="No cloud servers yet" />
      ) : (
        <table className="t">
          <thead>
            <tr>
              <th>Name</th>
              <th>Status</th>
              <th>Type</th>
              <th>Location</th>
              <th>IP</th>
              <th>Account</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {provisions.map((p) => (
              <tr key={p.id}>
                <td className="name">
                  {p.server_id ? <Link to={`/p/${project.id}/servers/${p.server_id}/overview`}>{p.name}</Link> : p.name}
                </td>
                <td>
                  <StatusText tone={p.status === 'ready' ? 'ok' : p.status === 'failed' ? 'err' : 'info'}>
                    {p.status.replace(/_/g, ' ')}
                  </StatusText>
                  {p.error && <div className="sub">{p.error}</div>}
                </td>
                <td className="mono muted">{p.server_type}</td>
                <td className="muted">{p.region}</td>
                <td className="mono muted">{p.ip || '—'}</td>
                <td className="muted">{p.account}</td>
                <td className="right">
                  <button className="btn ghost sm danger" onClick={() => setDestroying(p)}>
                    Destroy
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {creating && <CloudProvisionDialog accounts={accounts!} onClose={() => setCreating(false)} onDone={reload} />}
      {destroying && (
        <Confirm
          title={`Destroy ${destroying.name}?`}
          action="Destroy server"
          danger
          typeToConfirm={destroying.name}
          onClose={() => setDestroying(null)}
          onConfirm={() =>
            api('DELETE', `/projects/${project.id}/provisions/${destroying.id}`, { confirm: destroying.name }).then(
              reload,
            )
          }
        >
          Deletes the server and its disks at Hetzner Cloud and removes it from Orbit. Everything on it is lost and
          Hetzner stops charging for it.
        </Confirm>
      )}
    </Panel>
  )
}

function CloudProvisionDialog({
  accounts,
  onClose,
  onDone,
}: {
  accounts: { id: string; name: string }[]
  onClose: () => void
  onDone: () => void
}) {
  const { project } = useProject()
  const [account, setAccount] = useState(accounts[0].id)
  const [opts, optsErr] = useApi<{ locations: any[]; server_types: any[] }>(
    `/projects/${project.id}/cloud/options?account=${account}`,
  )
  const [f, setF] = useState({ name: '', location: '', server_type: '' })
  const [error, setError] = useState<string>()
  useEffect(() => {
    if (opts && !f.location)
      setF((x) => ({
        ...x,
        location: opts.locations[0]?.name ?? '',
        server_type: opts.server_types.find((t: any) => !t.deprecated)?.name ?? '',
      }))
  }, [opts, f.location])
  const price = opts?.server_types
    .find((t: any) => t.name === f.server_type)
    ?.prices?.find((p: any) => p.location === f.location)?.price_monthly?.gross
  const save = () =>
    api('POST', `/projects/${project.id}/provisions`, { ...f, account_id: account }).then(
      () => {
        onDone()
        onClose()
      },
      (e) => setError(e.message),
    )
  return (
    <Dialog
      title="Create server"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" disabled={!f.name || !f.location} onClick={save}>
            Create server
          </button>
        </>
      }
    >
      <label className="field">
        <span>Account</span>
        <select className="select" value={account} onChange={(e) => setAccount(e.target.value)}>
          {accounts.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name} (Hetzner Cloud)
            </option>
          ))}
        </select>
      </label>
      <ErrorNote error={optsErr} />
      <label className="field">
        <span>Name (hostname)</span>
        <input
          className="input mono"
          placeholder="app-03"
          value={f.name}
          onChange={(e) => setF({ ...f, name: e.target.value.toLowerCase() })}
          autoFocus
        />
      </label>
      <div className="grid-2">
        <label className="field">
          <span>Location</span>
          <select className="select" value={f.location} onChange={(e) => setF({ ...f, location: e.target.value })}>
            {opts?.locations.map((l: any) => (
              <option key={l.name} value={l.name}>
                {l.city} ({l.name})
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>Type</span>
          <select
            className="select"
            value={f.server_type}
            onChange={(e) => setF({ ...f, server_type: e.target.value })}
          >
            {opts?.server_types
              .filter((t: any) => !t.deprecated)
              .map((t: any) => (
                <option key={t.name} value={t.name}>
                  {t.name} · {t.cores} vCPU · {t.memory} GB · {t.disk} GB · {t.architecture}
                </option>
              ))}
          </select>
        </label>
      </div>
      <div className="muted" style={{ fontSize: 12 }}>
        Ubuntu 24.04 with Docker and the Orbit agent installed by cloud-init. It appears under Servers in 2–5 minutes,
        region <span className="mono">{f.location || '—'}</span>.
        {price && <> About €{(+price).toFixed(2)}/month at Hetzner.</>}
      </div>
      <ErrorNote error={error} />
    </Dialog>
  )
}
