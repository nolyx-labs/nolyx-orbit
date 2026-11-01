import { useState } from 'react'
import { useOrgCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { api, useApi } from '../../lib/api'
import { ago, datetime } from '../../lib/format'
import { KeyDialog } from './ApiKeyDialog'

export interface Key {
  id: string
  name: string
  prefix: string
  role: string
  project: string | null
  last_used_at: string | null
  expires_at: string | null
  created_at: string
  created_by: string
}

export function ApiKeys() {
  const { org, me } = useProject()
  const canAdmin = useOrgCan('admin')
  const [keys, error, reload] = useApi<Key[]>(canAdmin ? `/orgs/${org.id}/api-keys` : null)
  const [creating, setCreating] = useState(false)
  const [revoking, setRevoking] = useState<Key | null>(null)
  if (!canAdmin)
    return (
      <div className="page">
        <PageHead title="API Keys" />
        <Panel pad>
          <span className="muted">Managing API keys requires the admin role.</span>
        </Panel>
      </div>
    )
  return (
    <div className="page">
      <PageHead
        title="API Keys"
        sub={
          <>
            Use <span className="mono">Authorization: Bearer orb_key_…</span> against{' '}
            <span className="mono">/api/v1</span>. A key never exceeds its creator's role.
          </>
        }
      >
        <button className="btn primary" onClick={() => setCreating(true)}>
          Create key
        </button>
      </PageHead>
      <Panel>
        <ErrorNote error={error} />
        {!keys ? (
          <Loading />
        ) : !keys.length ? (
          <Empty title="No API keys" />
        ) : (
          <table className="t">
            <thead>
              <tr>
                <th>Name</th>
                <th>Key</th>
                <th>Role</th>
                <th>Scope</th>
                <th>Last used</th>
                <th>Expires</th>
                <th>Created by</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {keys.map((k) => (
                <tr key={k.id}>
                  <td className="name">{k.name}</td>
                  <td className="mono muted">{k.prefix}…</td>
                  <td>{k.role}</td>
                  <td className="muted">{k.project ?? 'All projects'}</td>
                  <td className="muted">{k.last_used_at ? ago(k.last_used_at) : 'never'}</td>
                  <td className="muted">{k.expires_at ? datetime(k.expires_at) : 'never'}</td>
                  <td className="muted">{k.created_by}</td>
                  <td className="right">
                    <button className="btn ghost sm danger" onClick={() => setRevoking(k)}>
                      Revoke
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
      {creating && (
        <KeyDialog
          projects={me.orgs.find((o) => o.id === org.id)!.projects}
          onClose={() => setCreating(false)}
          onSaved={reload}
        />
      )}
      {revoking && (
        <Confirm
          title={`Revoke ${revoking.name}?`}
          action="Revoke"
          danger
          onClose={() => setRevoking(null)}
          onConfirm={() => api('DELETE', `/orgs/${org.id}/api-keys/${revoking.id}`).then(reload)}
        >
          Requests using this key fail immediately.
        </Confirm>
      )}
    </div>
  )
}
