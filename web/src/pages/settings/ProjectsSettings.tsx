import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useOrgCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { Dialog } from '../../components/ui/Dialog'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { ErrorNote, Loading } from '../../components/ui/states'
import { api } from '../../lib/api'
import { datetime } from '../../lib/format'
import { useOrg } from './useOrg'

export function ProjectsSettings() {
  const { org, project, reloadMe } = useProject()
  const canAdmin = useOrgCan('admin')
  const nav = useNavigate()
  const [data, , reload] = useOrg()
  const [renaming, setRenaming] = useState<{ id: string; name: string } | null>(null)
  const [deleting, setDeleting] = useState<{ id: string; name: string } | null>(null)
  const [creating, setCreating] = useState('')
  const [error, setError] = useState<string>()
  return (
    <div className="page">
      <PageHead
        title="Projects"
        sub="Projects separate environments such as Production and Staging. Secrets, servers and services are per project."
      />
      <ErrorNote error={error} />
      <Panel actions={null}>
        {!data ? (
          <Loading />
        ) : (
          <table className="t">
            <thead>
              <tr>
                <th>Project</th>
                <th className="right">Servers</th>
                <th className="right">Services</th>
                <th className="right">Databases</th>
                <th>Created</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {data.projects.map((p) => (
                <tr key={p.id}>
                  <td className="name">
                    <Link to={`/p/${p.id}/overview`}>{p.name}</Link>
                    {p.id === project.id && <span className="faint"> · current</span>}
                  </td>
                  <td className="right mono">{p.servers}</td>
                  <td className="right mono">{p.apps}</td>
                  <td className="right mono">{p.databases}</td>
                  <td className="muted">{datetime(p.created_at)}</td>
                  <td className="right">
                    {canAdmin && (
                      <>
                        <button className="btn ghost sm" onClick={() => setRenaming(p)}>
                          Rename
                        </button>
                        <button className="btn ghost sm danger" onClick={() => setDeleting(p)}>
                          Delete
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {canAdmin && (
          <div className="panel-body row" style={{ borderTop: '1px solid var(--border)' }}>
            <input
              className="input"
              style={{ maxWidth: 260 }}
              placeholder="New project name"
              value={creating}
              onChange={(e) => setCreating(e.target.value)}
            />
            <button
              className="btn"
              disabled={!creating.trim()}
              onClick={() =>
                api<{ id: string }>('POST', `/orgs/${org.id}/projects`, { name: creating }).then(
                  () => {
                    setCreating('')
                    reload()
                    reloadMe()
                  },
                  (e) => setError(e.message),
                )
              }
            >
              Create project
            </button>
          </div>
        )}
      </Panel>
      {renaming && (
        <Dialog
          title="Rename project"
          onClose={() => setRenaming(null)}
          footer={
            <button
              className="btn primary"
              onClick={() =>
                api('PATCH', `/projects/${renaming.id}`, { name: renaming.name }).then(
                  () => {
                    setRenaming(null)
                    reload()
                    reloadMe()
                  },
                  (e) => setError(e.message),
                )
              }
            >
              Save
            </button>
          }
        >
          <input
            className="input"
            value={renaming.name}
            onChange={(e) => setRenaming({ ...renaming, name: e.target.value })}
            autoFocus
          />
        </Dialog>
      )}
      {deleting && (
        <Confirm
          title={`Delete ${deleting.name}?`}
          action="Delete project"
          danger
          typeToConfirm={deleting.name}
          onClose={() => setDeleting(null)}
          onConfirm={() =>
            api('DELETE', `/projects/${deleting.id}`, { confirm: deleting.name }).then(() => {
              reloadMe()
              if (deleting.id === project.id) nav('/')
              else reload()
            })
          }
        >
          Remove its servers first. Deletes the project's services, databases records, secrets, alerts and history.
        </Confirm>
      )}
    </div>
  )
}
