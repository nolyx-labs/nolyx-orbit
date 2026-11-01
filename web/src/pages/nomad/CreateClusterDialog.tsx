import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useProject } from '../../components/layout/context'
import { Dialog } from '../../components/ui/Dialog'
import { ErrorNote } from '../../components/ui/states'
import { Badge } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { opPath } from '../../lib/paths'
import { Server } from '../../lib/types'

export function CreateCluster({ onClose }: { onClose: () => void }) {
  const { project } = useProject()
  const nav = useNavigate()
  const [servers] = useApi<Server[]>(`/projects/${project.id}/servers`)
  const [picked, setPicked] = useState<string[]>([])
  const [f, setF] = useState({ servers: 1, datacenter: 'dc1', region: 'global', version: '1.10' })
  const [error, setError] = useState<string>()
  const create = () =>
    api<{ operation_ids: string[] }>('POST', `/projects/${project.id}/nomad/clusters`, {
      ...f,
      server_ids: picked,
    }).then(
      (r) => nav(opPath(project.id, r.operation_ids[0])),
      (e) => setError(e.message),
    )
  return (
    <Dialog
      title="Create Nomad cluster"
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" disabled={!picked.length} onClick={create}>
            Install Nomad on {picked.length} server(s)
          </button>
        </>
      }
    >
      <div className="muted">
        Orbit runs the official Nomad image on each selected server (Docker task driver). The first {f.servers} selected
        server(s) also run the Nomad server role. Use 3 for fault tolerance.
      </div>
      <div
        className="list"
        style={{
          border: '1px solid var(--border)',
          borderRadius: 'var(--radius-sm)',
          maxHeight: 240,
          overflow: 'auto',
        }}
      >
        {servers?.map((s) => (
          <label key={s.id} className="list-item" style={{ cursor: 'pointer' }}>
            <input
              type="checkbox"
              checked={picked.includes(s.id)}
              disabled={s.status !== 'online' || !!s.nomad_version}
              onChange={(e) => setPicked(e.target.checked ? [...picked, s.id] : picked.filter((x) => x !== s.id))}
            />
            <span style={{ flex: 1 }}>{s.name}</span>
            {picked.indexOf(s.id) >= 0 && picked.indexOf(s.id) < f.servers && (
              <Badge tone="info">server + client</Badge>
            )}
            {picked.indexOf(s.id) >= f.servers && <Badge>client</Badge>}
            {s.nomad_version && <span className="faint">Nomad {s.nomad_version} already running</span>}
            <span className="mono faint">{s.ip}</span>
          </label>
        ))}
      </div>
      <div className="grid-2">
        <label className="field">
          <span>Server role on</span>
          <select className="select" value={f.servers} onChange={(e) => setF({ ...f, servers: +e.target.value })}>
            <option value={1}>1 server (no HA)</option>
            <option value={3}>3 servers (tolerates 1 failure)</option>
          </select>
        </label>
        <label className="field">
          <span>Nomad version</span>
          <input className="input mono" value={f.version} onChange={(e) => setF({ ...f, version: e.target.value })} />
        </label>
        <label className="field">
          <span>Region</span>
          <input className="input mono" value={f.region} onChange={(e) => setF({ ...f, region: e.target.value })} />
        </label>
        <label className="field">
          <span>Datacenter</span>
          <input
            className="input mono"
            value={f.datacenter}
            onChange={(e) => setF({ ...f, datacenter: e.target.value })}
          />
        </label>
      </div>
      <div className="callout warn">
        Nomad's ports (4646–4648) must be reachable between the selected servers. Nomad in Docker supports the Docker
        driver only; HashiCorp recommends native installs for exec/raw_exec.
      </div>
      <ErrorNote error={error} />
    </Dialog>
  )
}
