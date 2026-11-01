import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { Badge } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { ago } from '../../lib/format'

interface Release {
  app_id: string
  app: string
  image: string
  number: number
  commit_sha: string
  commit_message: string
  created_at: string
  current: boolean
}

export function Releases() {
  const { project, org } = useProject()
  const canMember = useCan('member')
  const nav = useNavigate()
  const [rels] = useApi<Release[]>(`/projects/${project.id}/releases`)
  const [rollback, setRollback] = useState<Release | null>(null)
  const [promote, setPromote] = useState<Release | null>(null)
  const [target, setTarget] = useState('')
  const others = org.projects.filter((p) => p.id !== project.id)
  const apps = [...new Set(rels?.map((r) => r.app))]
  return (
    <div className="page">
      <PageHead
        title="Releases"
        sub="Every image that has deployed successfully. Roll back, or promote a release to another project."
      />
      {!rels ? (
        <Loading />
      ) : !rels.length ? (
        <Panel>
          <Empty title="No releases yet">A release is recorded when a deployment succeeds.</Empty>
        </Panel>
      ) : (
        <div className="stack">
          {apps.map((app) => (
            <Panel key={app} title={app}>
              <div className="list">
                {rels
                  .filter((r) => r.app === app)
                  .map((r) => (
                    <div key={r.image} className="list-item">
                      <span
                        className="mono"
                        style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis' }}
                      >
                        {r.image}
                      </span>
                      {r.commit_sha && (
                        <span className="mono faint">
                          {r.commit_sha.slice(0, 7)} {r.commit_message}
                        </span>
                      )}
                      <span className="faint">
                        #{r.number} · {ago(r.created_at)}
                      </span>
                      {r.current ? (
                        <Badge tone="ok">current</Badge>
                      ) : (
                        canMember && (
                          <button className="btn sm" onClick={() => setRollback(r)}>
                            Roll back to this
                          </button>
                        )
                      )}
                      {canMember && others.length > 0 && !r.image.startsWith('orbit/') && (
                        <button
                          className="btn sm"
                          onClick={() => {
                            setPromote(r)
                            setTarget(others[0].id)
                          }}
                        >
                          Promote…
                        </button>
                      )}
                    </div>
                  ))}
              </div>
            </Panel>
          ))}
        </div>
      )}
      {rollback && (
        <Confirm
          title={`Roll back ${rollback.app}?`}
          action="Deploy this release"
          onClose={() => setRollback(null)}
          onConfirm={() =>
            api<{ id: string }>('POST', `/projects/${project.id}/apps/${rollback.app_id}/deploy`, {
              image: rollback.image,
            }).then((d) => nav(`/p/${project.id}/deployments/${d.id}`))
          }
        >
          Deploys <span className="mono">{rollback.image}</span> with the service's current configuration and secrets.
        </Confirm>
      )}
      {promote && (
        <Confirm
          title={`Promote ${promote.app}`}
          action="Promote"
          onClose={() => setPromote(null)}
          onConfirm={() =>
            api<{ id: string; project_id: string }>('POST', `/projects/${project.id}/apps/${promote.app_id}/promote`, {
              project_id: target,
              image: promote.image,
            }).then((d) => nav(`/p/${d.project_id}/deployments/${d.id}`))
          }
        >
          Deploy <span className="mono">{promote.image}</span> to the service named <b>{promote.app}</b> in:
          <select
            className="select"
            style={{ marginTop: 8 }}
            value={target}
            onChange={(e) => setTarget(e.target.value)}
          >
            {others.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
          <div className="faint" style={{ marginTop: 6 }}>
            That project must already have a service with this name.{' '}
            <Link to={`/p/${project.id}/services`}>Services</Link>
          </div>
        </Confirm>
      )}
    </div>
  )
}
