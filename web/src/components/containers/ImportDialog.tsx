import { useState } from 'react'
import { useNavigate } from 'react-router'
import { api } from '../../lib/api'
import { Container } from '../../lib/types'
import { useProject } from '../layout/context'
import { Dialog } from '../ui/Dialog'
import { ErrorNote } from '../ui/states'

/** A container Orbit didn't create, and that isn't a managed database or Orbit itself. */
export const importable = (c: Container) => !Object.keys(c.labels).some((k) => k.startsWith('orbit.'))

/** A service name from the Compose service or container name: lowercase letters, digits and dashes. */
export const serviceName = (c: Container) =>
  (c.labels['com.docker.compose.service'] || c.name)
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 63)

export function ImportDialog({ c, onClose }: { c: Container; onClose: () => void }) {
  const { project } = useProject()
  const nav = useNavigate()
  const [name, setName] = useState(serviceName(c))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const compose = c.labels['com.docker.compose.project']
  const go = () => {
    setBusy(true)
    setError(undefined)
    api<{ deployment_id: string }>('POST', `/projects/${project.id}/servers/${c.server_id}/containers/${c.id}/import`, {
      name,
    }).then(
      (r) => nav(`/p/${project.id}/deployments/${r.deployment_id}`),
      (e) => {
        setError(e.message)
        setBusy(false)
      },
    )
  }
  return (
    <Dialog
      title={`Import ${c.name} as a service`}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" onClick={go} disabled={busy || !name}>
            {busy ? 'Importing…' : 'Import and deploy'}
          </button>
        </>
      }
    >
      <div className="muted">
        Orbit copies this container's image, ports, environment, command, volumes, folders, networks and labels into a
        new service, then deploys it in place of the container. Its data stays where it is. After that you get deploys,
        rollbacks, backups and alerts for it.
      </div>
      <ul className="muted" style={{ margin: 0, paddingLeft: 18 }}>
        <li>
          There's a short interruption while the container is swapped. If the new one doesn't start, the original is
          started again.
        </li>
        <li>
          The original is kept, stopped, as <span className="mono">{c.name}-before-orbit</span>. Remove it once you're
          happy.
        </li>
        <li>Anything written inside the container rather than to a volume or folder isn't copied.</li>
        {compose && (
          <li>
            It's part of the Compose project <span className="mono">{compose}</span> and stays reachable there by its
            old names. Don't run <span className="mono">docker compose up</span> for it again, or Compose starts a
            second copy.
          </li>
        )}
      </ul>
      <label className="field">
        <span>Service name</span>
        <input className="input mono" value={name} onChange={(e) => setName(e.target.value)} autoFocus />
      </label>
      <ErrorNote error={error} />
    </Dialog>
  )
}
