import { useState } from 'react'
import { useCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, Loading } from '../../components/ui/states'
import { Badge } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { ago } from '../../lib/format'
import { SecretDialog } from './SecretDialog'

interface Secret {
  id: string | null
  key: string
  scope: string
  updated_at: string
  updated_by: string | null
}

export function Secrets() {
  const { project, org } = useProject()
  const canMember = useCan('member')
  const [rows, , reload] = useApi<Secret[]>(`/projects/${project.id}/secrets`)
  const [editing, setEditing] = useState<{ key: string; scope: string } | null>(null)
  const [deleting, setDeleting] = useState<Secret | null>(null)
  const groups: [string, string][] = [
    ['organization', `Organization · ${org.name}`],
    ['project', `Project · ${project.name}`],
  ]
  return (
    <div className="page">
      <PageHead
        title="Secrets"
        sub="Encrypted at rest and injected at deploy. Merge order: organization → project → service (later wins)."
      >
        {canMember && (
          <button className="btn primary" onClick={() => setEditing({ key: '', scope: 'project' })}>
            Add secret
          </button>
        )}
      </PageHead>
      {!rows ? (
        <Loading />
      ) : (
        <div className="stack">
          {groups.map(([scope, title]) => {
            const list = rows.filter((r) => r.scope === scope)
            return (
              <Panel key={scope} title={title}>
                {!list.length ? (
                  <Empty title="No secrets" />
                ) : (
                  <div className="list">
                    {list.map((r) => (
                      <div key={r.key} className="list-item">
                        <span className="mono" style={{ fontWeight: 500 }}>
                          {r.key}
                        </span>
                        <span className="mono faint">••••••••</span>
                        <span className="spacer" />
                        <span className="faint" style={{ fontSize: 12 }}>
                          updated {ago(r.updated_at)}
                          {r.updated_by && ` by ${r.updated_by}`}
                        </span>
                        {canMember && (
                          <button className="btn ghost sm" onClick={() => setEditing({ key: r.key, scope })}>
                            Replace
                          </button>
                        )}
                        {canMember && (
                          <button className="btn ghost sm danger" onClick={() => setDeleting(r)}>
                            Delete
                          </button>
                        )}
                      </div>
                    ))}
                  </div>
                )}
              </Panel>
            )
          })}
          <Panel title="Service variables">
            {!rows.some((r) => r.scope.startsWith('application:')) ? (
              <Empty title="No service variables" />
            ) : (
              <div className="list">
                {rows
                  .filter((r) => r.scope.startsWith('application:'))
                  .map((r) => (
                    <div key={r.scope + r.key} className="list-item">
                      <span className="mono">{r.key}</span>
                      <span className="mono faint">••••••••</span>
                      <span className="spacer" />
                      <Badge>{r.scope.replace('application:', '')}</Badge>
                    </div>
                  ))}
              </div>
            )}
            <div className="panel-body faint" style={{ fontSize: 12 }}>
              Edit these from each service. Values are write-only everywhere: Orbit never displays a secret after it is
              saved.
            </div>
          </Panel>
        </div>
      )}
      {editing && <SecretDialog initial={editing} onClose={() => setEditing(null)} onSaved={reload} />}
      {deleting && (
        <Confirm
          title={`Delete ${deleting.key}?`}
          action="Delete"
          danger
          onClose={() => setDeleting(null)}
          onConfirm={() => api('DELETE', `/projects/${project.id}/secrets/${deleting.id}`).then(reload)}
        >
          Services keep the value until their next deploy, after which it is no longer injected.
        </Confirm>
      )}
    </div>
  )
}
