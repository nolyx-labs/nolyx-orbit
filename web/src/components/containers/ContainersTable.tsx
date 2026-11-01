import { useState } from 'react'
import { bytes } from '../../lib/format'
import { Container } from '../../lib/types'
import { Empty } from '../ui/states'
import { StatusText, containerTone } from '../ui/status'
import { ContainerDialog } from './ContainerDialog'

export function ContainersTable({
  containers,
  showServer,
  onChanged,
}: {
  containers: Container[]
  showServer?: boolean
  onChanged: () => void
}) {
  const [open, setOpen] = useState<Container | null>(null)
  if (!containers.length)
    return <Empty title="No containers">Containers appear here within ~15 seconds of the agent connecting.</Empty>
  return (
    <>
      <div className="table-wrap">
        <table className="t">
          <thead>
            <tr>
              <th>Name</th>
              {showServer && <th>Server</th>}
              <th>Status</th>
              <th>Image</th>
              <th>Ports</th>
              <th className="right">CPU</th>
              <th className="right">Memory</th>
              <th className="right">Restarts</th>
            </tr>
          </thead>
          <tbody>
            {containers.map((c) => (
              <tr key={c.server_id + c.id} className="link" onClick={() => setOpen(c)}>
                <td>
                  <div className="name">{c.name}</div>
                  <div className="sub mono">
                    {c.id.slice(0, 12)}
                    {c.labels['orbit.app'] && <> · app {c.labels['orbit.app']}</>}
                  </div>
                </td>
                {showServer && <td className="muted">{c.server_name}</td>}
                <td>
                  <StatusText tone={containerTone(c)}>
                    <span>{c.health ? `${c.state} · ${c.health}` : c.state}</span>
                  </StatusText>
                  <div className="sub">{c.status}</div>
                </td>
                <td
                  className="mono muted"
                  style={{ maxWidth: 280, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                  title={c.image}
                >
                  {c.image}
                </td>
                <td className="mono muted">{c.ports.join(', ') || '—'}</td>
                <td className="right mono">{c.state === 'running' ? `${c.cpu.toFixed(1)}%` : '—'}</td>
                <td className="right mono">{c.state === 'running' ? bytes(c.mem_used) : '—'}</td>
                <td className="right mono">
                  {c.restart_count > 0 ? <span style={{ color: 'var(--warn)' }}>{c.restart_count}</span> : 0}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {open && <ContainerDialog c={open} onClose={() => setOpen(null)} onChanged={onChanged} />}
    </>
  )
}
