import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useCan, useProject } from '../../components/layout/context'
import { Dialog } from '../../components/ui/Dialog'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { Badge, StatusText } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { COMPONENT_STATUSES } from './statusHelpers'

interface Row {
  id: string
  name: string
  slug: string
  domain: string | null
  public: boolean
  url: string
  components: number
  worst: number // 1-based rank, see BY_RANK
  open_incidents: number
  subscribers: number
}

// The server ranks statuses operational, under_maintenance, degraded, partial, major (1-based).
const BY_RANK = ['operational', 'under_maintenance', 'degraded_performance', 'partial_outage', 'major_outage']

export function StatusPages() {
  const { project } = useProject()
  const nav = useNavigate()
  const canAdmin = useCan('admin')
  const [pages] = useApi<Row[]>(`/projects/${project.id}/status-pages`)
  const [adding, setAdding] = useState(false)
  return (
    <div className="page">
      <PageHead
        title="Status pages"
        sub="Public pages that show your services' health, incidents and planned maintenance"
      >
        {canAdmin && (
          <button className="btn primary" onClick={() => setAdding(true)}>
            New status page
          </button>
        )}
      </PageHead>
      <Panel>
        {!pages ? (
          <Loading />
        ) : !pages.length ? (
          <Empty title="No status pages yet">
            A status page lists components (linked to monitors, servers or services, or set by hand) with 90 days of
            uptime, and the incidents and maintenance you post. Visitors can subscribe by email, webhook or feed.
          </Empty>
        ) : (
          <table className="t">
            <thead>
              <tr>
                <th>Page</th>
                <th>Status</th>
                <th className="right">Components</th>
                <th className="right">Open incidents</th>
                <th className="right">Subscribers</th>
              </tr>
            </thead>
            <tbody>
              {pages.map((p) => {
                const [, label, tone] =
                  COMPONENT_STATUSES.find((s) => s[0] === BY_RANK[p.worst - 1]) ?? COMPONENT_STATUSES[0]
                return (
                  <tr
                    key={p.id}
                    className="link"
                    onClick={() => nav(`/p/${project.id}/status-pages/${p.id}/incidents`)}
                  >
                    <td>
                      <div className="name">
                        {p.name} {!p.public && <Badge>private</Badge>}
                      </div>
                      <div className="sub mono">{p.domain ?? p.url.replace(/^https?:\/\//, '')}</div>
                    </td>
                    <td>
                      <StatusText tone={p.components ? tone : ''}>{p.components ? label : 'No components'}</StatusText>
                    </td>
                    <td className="right mono">{p.components}</td>
                    <td className="right mono">{p.open_incidents || '—'}</td>
                    <td className="right mono">{p.subscribers}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </Panel>
      {adding && (
        <NewPageDialog
          onClose={() => setAdding(false)}
          onCreated={(id) => nav(`/p/${project.id}/status-pages/${id}/components`)}
        />
      )}
    </div>
  )
}

function NewPageDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (id: string) => void }) {
  const { project } = useProject()
  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [touched, setTouched] = useState(false)
  const [error, setError] = useState<string>()
  const auto = (n: string) =>
    n
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 48)
  return (
    <Dialog
      title="New status page"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            disabled={!name || !slug}
            onClick={() =>
              api<{ id: string }>('POST', `/projects/${project.id}/status-pages`, { name, slug }).then(
                (r) => onCreated(r.id),
                (e) => setError(e.message),
              )
            }
          >
            Create
          </button>
        </>
      }
    >
      <label className="field">
        <span>Name</span>
        <input
          className="input"
          value={name}
          placeholder="Acme Status"
          autoFocus
          onChange={(e) => {
            setName(e.target.value)
            if (!touched) setSlug(auto(e.target.value))
          }}
        />
      </label>
      <label className="field">
        <span>Address</span>
        <div className="row" style={{ gap: 6 }}>
          <span className="mono muted">{location.origin}/status/</span>
          <input
            className="input mono"
            value={slug}
            onChange={(e) => {
              setTouched(true)
              setSlug(e.target.value)
            }}
          />
        </div>
        <span className="hint">You can add your own domain, such as status.example.com, in the page's settings.</span>
      </label>
      <ErrorNote error={error} />
    </Dialog>
  )
}
