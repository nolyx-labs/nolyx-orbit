import { useState } from 'react'
import { useOrgCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { Dialog } from '../../components/ui/Dialog'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { Badge } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { ago } from '../../lib/format'
import { useOrg } from './useOrg'

export function OrganizationSettings() {
  const { org, reloadMe } = useProject()
  const canAdmin = useOrgCan('admin')
  const isOwner = useOrgCan('owner')
  const [name, setName] = useState(org.name)
  const [saved, setSaved] = useState(false)
  const [accounts, , reloadAccounts] = useApi<{ id: string; provider: string; name: string; created_at: string }[]>(
    canAdmin ? `/orgs/${org.id}/cloud-accounts` : null,
  )
  const [adding, setAdding] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [error, setError] = useState<string>()
  const [sso] = useApi<{ enabled: boolean; name: string }>('/auth/oidc')
  const [orgData, , reloadOrg] = useOrg()
  return (
    <div className="page">
      <PageHead title="Organization" />
      <div className="stack">
        <Panel title="General">
          <div className="panel-body row">
            <input
              className="input"
              style={{ maxWidth: 360 }}
              value={name}
              disabled={!canAdmin}
              onChange={(e) => {
                setName(e.target.value)
                setSaved(false)
              }}
            />
            {canAdmin && (
              <button
                className="btn"
                disabled={name === org.name}
                onClick={() =>
                  api('PATCH', `/orgs/${org.id}`, { name }).then(
                    () => {
                      setSaved(true)
                      reloadMe()
                    },
                    (e) => setError(e.message),
                  )
                }
              >
                Save
              </button>
            )}
            {saved && <span className="faint">Saved</span>}
          </div>
        </Panel>
        <Panel title="Security">
          <div className="panel-body row">
            <div style={{ flex: 1 }}>
              <div style={{ fontWeight: 500 }}>Require two-factor authentication</div>
              <div className="muted">
                Members without it are locked out of {org.name} until they turn it on. You can also require it for
                specific teams in Security → Access.
              </div>
            </div>
            {canAdmin ? (
              <button
                className={org.require_mfa ? 'btn' : 'btn primary'}
                onClick={() =>
                  api('PATCH', `/orgs/${org.id}`, { require_mfa: !org.require_mfa }).then(reloadMe, (e) =>
                    setError(e.message),
                  )
                }
              >
                {org.require_mfa ? 'Stop requiring' : 'Require'}
              </button>
            ) : (
              <span className="faint">{org.require_mfa ? 'Required' : 'Not required'}</span>
            )}
          </div>
          {sso?.enabled && orgData && (
            <div className="panel-body row">
              <div style={{ flex: 1 }}>
                <div style={{ fontWeight: 500 }}>Require single sign-on</div>
                <div className="muted">
                  Members must sign in with {sso.name} instead of a password. Owners can still use their password, so
                  you aren't locked out if {sso.name} is unavailable.
                </div>
              </div>
              {isOwner ? (
                <button
                  className={orgData.org.require_sso ? 'btn' : 'btn primary'}
                  onClick={() =>
                    api('PATCH', `/orgs/${org.id}`, { require_sso: !orgData.org.require_sso }).then(reloadOrg, (e) =>
                      setError(e.message),
                    )
                  }
                >
                  {orgData.org.require_sso ? 'Stop requiring' : 'Require'}
                </button>
              ) : (
                <span className="faint">{orgData.org.require_sso ? 'Required' : 'Not required'}</span>
              )}
            </div>
          )}
          {orgData && (
            <div className="panel-body row">
              <div style={{ flex: 1 }}>
                <div style={{ fontWeight: 500 }}>Keep the audit log</div>
                <div className="muted">
                  Older entries are deleted for good. Export the log from Security → Access first if you need a copy.
                </div>
              </div>
              <select
                className="select"
                style={{ width: 140 }}
                disabled={!isOwner}
                value={orgData.org.audit_retention_days}
                onChange={(e) =>
                  api('PATCH', `/orgs/${org.id}`, { audit_retention_days: +e.target.value }).then(reloadOrg, (e) =>
                    setError(e.message),
                  )
                }
              >
                {[
                  [0, 'Forever'],
                  [730, '2 years'],
                  [365, '1 year'],
                  [180, '180 days'],
                  [90, '90 days'],
                ].map(([v, l]) => (
                  <option key={v} value={v}>
                    {l}
                  </option>
                ))}
              </select>
            </div>
          )}
        </Panel>
        {canAdmin && (
          <Panel
            title="Cloud accounts"
            actions={
              <button className="btn sm" onClick={() => setAdding(true)}>
                Connect Hetzner Cloud
              </button>
            }
          >
            {!accounts ? (
              <Loading />
            ) : !accounts.length ? (
              <Empty title="No cloud accounts">Connect one to create servers from Settings → Agents.</Empty>
            ) : (
              <div className="list">
                {accounts.map((a) => (
                  <div key={a.id} className="list-item">
                    <span style={{ fontWeight: 500 }}>{a.name}</span>
                    <Badge>{a.provider}</Badge>
                    <span className="spacer" />
                    <span className="faint">added {ago(a.created_at)}</span>
                    <button
                      className="btn ghost sm danger"
                      onClick={() => api('DELETE', `/orgs/${org.id}/cloud-accounts/${a.id}`).then(reloadAccounts)}
                    >
                      Remove
                    </button>
                  </div>
                ))}
              </div>
            )}
            <div className="panel-body faint" style={{ fontSize: 12 }}>
              API tokens are encrypted at rest. Use a project-scoped Hetzner token with read &amp; write access.
            </div>
          </Panel>
        )}
        {isOwner && (
          <Panel title="Danger zone">
            <div className="panel-body row">
              <div style={{ flex: 1 }}>
                <div style={{ fontWeight: 500 }}>Delete organization</div>
                <div className="muted">
                  Remove every server first. Deletes all projects, services, databases records, secrets and audit
                  history.
                </div>
              </div>
              <button className="btn danger" onClick={() => setDeleting(true)}>
                Delete
              </button>
            </div>
          </Panel>
        )}
        <ErrorNote error={error} />
      </div>
      {adding && <CloudAccountDialog onClose={() => setAdding(false)} onDone={reloadAccounts} />}
      {deleting && (
        <Confirm
          title={`Delete ${org.name}?`}
          action="Delete organization"
          danger
          typeToConfirm={org.name}
          onClose={() => setDeleting(false)}
          onConfirm={() =>
            api('DELETE', `/orgs/${org.id}`, { confirm: org.name }).then(() => (window.location.href = '/'))
          }
        >
          This cannot be undone.
        </Confirm>
      )}
    </div>
  )
}

function CloudAccountDialog({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { org } = useProject()
  const [f, setF] = useState({ name: 'Hetzner', token: '' })
  const [error, setError] = useState<string>()
  const save = () =>
    api('POST', `/orgs/${org.id}/cloud-accounts`, f).then(
      () => {
        onDone()
        onClose()
      },
      (e) => setError(e.message),
    )
  return (
    <Dialog
      title="Connect Hetzner Cloud"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" disabled={!f.token} onClick={save}>
            Connect
          </button>
        </>
      }
    >
      <label className="field">
        <span>Name</span>
        <input className="input" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
      </label>
      <label className="field">
        <span>API token</span>
        <input
          className="input mono"
          type="password"
          value={f.token}
          onChange={(e) => setF({ ...f, token: e.target.value })}
          autoComplete="new-password"
          autoFocus
        />
        <span className="hint">Hetzner Console → Security → API tokens. Orbit checks it before saving.</span>
      </label>
      <ErrorNote error={error} />
    </Dialog>
  )
}
