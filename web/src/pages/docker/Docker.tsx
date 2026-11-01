import { useState } from 'react'
import { ContainersTable } from '../../components/containers/ContainersTable'
import { useProject } from '../../components/layout/context'
import { Segmented } from '../../components/ui/controls'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Loading } from '../../components/ui/states'
import { useApi } from '../../lib/api'
import { useLive } from '../../lib/live'
import { Container, Server } from '../../lib/types'

const STATES = ['all', 'running', 'stopped'] as const

export function Docker() {
  const { project } = useProject()
  const [server, setServer] = useState('')
  const [state, setState] = useState<(typeof STATES)[number]>('all')
  const [q, setQ] = useState('')
  const [cs, , reload] = useApi<Container[]>(`/projects/${project.id}/containers?server=${server}`)
  const [servers] = useApi<Server[]>(`/projects/${project.id}/servers`)
  useLive(project.id, ['containers'], reload)

  const shown = cs?.filter(
    (c) =>
      (state === 'all' || (state === 'running') === (c.state === 'running')) &&
      (!q || `${c.name} ${c.image}`.toLowerCase().includes(q.toLowerCase())),
  )

  return (
    <div className="page">
      <PageHead title="Docker" sub="Containers across all servers" />
      <div className="row" style={{ marginBottom: 12 }}>
        <input
          className="input"
          placeholder="Search name or image…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          style={{ maxWidth: 280 }}
        />
        <select
          className="select"
          style={{ width: 200 }}
          value={server}
          onChange={(e) => setServer(e.target.value)}
          aria-label="Server"
        >
          <option value="">All servers</option>
          {servers?.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
        <Segmented label="State" options={STATES} value={state} onChange={setState} />
        <div className="spacer" />
        {cs && (
          <span className="faint">
            {shown!.length} of {cs.length}
          </span>
        )}
      </div>
      <Panel>{shown ? <ContainersTable containers={shown} showServer onChanged={reload} /> : <Loading />}</Panel>
    </div>
  )
}
