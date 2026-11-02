import { useState } from 'react'
import { useOrgCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { Badge } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { datetime } from '../../lib/format'
import { OrgData, useOrg } from '../settings/useOrg'
import { InviteDialog } from './InviteDialog'
import { AREA_LABELS, CustomRole, RoleDialog } from './RoleDialog'
import { TeamDialog } from './TeamDialog'

interface Audit {
  id: number
  actor: string
  action: string
  target: string
  metadata: Record<string, unknown>
  ip: string
  created_at: string
  project: string | null
}

export function Access() {
  const { org, me, reloadMe } = useProject()
  const canAdmin = useOrgCan('admin')
  const [data, , reload] = useOrg()
  const [rows] = useApi<Audit[]>(canAdmin ? `/orgs/${org.id}/audit` : null)
  const [roles, , reloadRoles] = useApi<{ areas: string[]; roles: CustomRole[] }>(`/orgs/${org.id}/roles`)
  const [roleEdit, setRoleEdit] = useState<CustomRole | 'new' | null>(null)
  const [q, setQ] = useState('')
  const [inviting, setInviting] = useState(false)
  const [teamEdit, setTeamEdit] = useState<OrgData['teams'][number] | 'new' | null>(null)
  const [removing, setRemoving] = useState<OrgData['members'][number] | null>(null)
  const [err, setErr] = useState<string>()
  const shown = rows?.filter((r) => !q || `${r.actor} ${r.action} ${r.target}`.toLowerCase().includes(q.toLowerCase()))
  const setRole = (uid: string, body: { role: string } | { custom_role_id: string }) =>
    api('PATCH', `/orgs/${org.id}/members/${uid}`, body).then(
      () => {
        reload()
        reloadMe()
      },
      (e) => setErr(e.message),
    )
  return (
    <div className="page">
      <PageHead title="Access" sub={`Members, teams and the audit log for ${org.name}`} />
      <ErrorNote error={err} />
      {!data ? (
        <Loading />
      ) : (
        <div className="stack">
          <Panel
            title="Members"
            actions={
              canAdmin && (
                <button className="btn sm primary" onClick={() => setInviting(true)}>
                  Invite
                </button>
              )
            }
          >
            <table className="t">
              <thead>
                <tr>
                  <th>Member</th>
                  <th>Role</th>
                  <th>Custom role</th>
                  <th>2FA</th>
                  <th>Teams</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {data.members.map((m) => (
                  <tr key={m.id}>
                    <td>
                      <div className="name">{m.name}</div>
                      <div className="sub">{m.email}</div>
                    </td>
                    <td>
                      {canAdmin && m.id !== me.user.id ? (
                        <select
                          className="select"
                          style={{ width: 120 }}
                          value={m.role}
                          onChange={(e) => setRole(m.id, { role: e.target.value })}
                        >
                          {['viewer', 'member', 'admin', ...(data.role === 'owner' ? ['owner'] : [])].map((r) => (
                            <option key={r}>{r}</option>
                          ))}
                          {m.role === 'owner' && data.role !== 'owner' && <option>owner</option>}
                        </select>
                      ) : (
                        <span className="mono">{m.role}</span>
                      )}
                    </td>
                    <td>
                      {m.role === 'owner' ? (
                        <span className="faint">—</span>
                      ) : canAdmin && roles?.roles.length ? (
                        <select
                          className="select"
                          style={{ width: 140 }}
                          value={m.custom_role_id ?? ''}
                          onChange={(e) => setRole(m.id, { custom_role_id: e.target.value })}
                        >
                          <option value="">None</option>
                          {roles.roles.map((r) => (
                            <option key={r.id} value={r.id}>
                              {r.name}
                            </option>
                          ))}
                        </select>
                      ) : (
                        <span className="muted">
                          {roles?.roles.find((r) => r.id === m.custom_role_id)?.name ?? '—'}
                        </span>
                      )}
                    </td>
                    <td>{m.mfa_enabled ? <Badge tone="ok">on</Badge> : <Badge>off</Badge>}</td>
                    <td className="muted">{m.teams.join(', ') || '—'}</td>
                    <td className="right">
                      {(canAdmin || m.id === me.user.id) && (
                        <button className="btn ghost sm danger" onClick={() => setRemoving(m)}>
                          {m.id === me.user.id ? 'Leave' : 'Remove'}
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {data.invites.length > 0 && (
              <div className="list" style={{ borderTop: '1px solid var(--border)' }}>
                {data.invites.map((i) => (
                  <div key={i.id} className="list-item">
                    <Badge>invited</Badge>
                    <span>{i.email}</span>
                    <span className="mono faint">{i.role}</span>
                    <span className="spacer" />
                    <span className="faint" style={{ fontSize: 12 }}>
                      by {i.invited_by} · expires {datetime(i.expires_at)}
                    </span>
                    {canAdmin && (
                      <button
                        className="btn ghost sm danger"
                        onClick={() => api('DELETE', `/orgs/${org.id}/invites/${i.id}`).then(reload)}
                      >
                        Revoke
                      </button>
                    )}
                  </div>
                ))}
              </div>
            )}
            <div className="panel-body faint" style={{ fontSize: 12 }}>
              <b>owner</b> manages owners and can delete the organization · <b>admin</b> adds/removes servers, databases
              and members · <b>member</b> deploys and operates · <b>viewer</b> reads. Teams can raise a member's role on
              specific projects. A custom role narrows a member's role per area.
            </div>
          </Panel>
          <Panel
            title="Teams"
            actions={
              canAdmin && (
                <button className="btn sm" onClick={() => setTeamEdit('new')}>
                  New team
                </button>
              )
            }
          >
            {!data.teams.length ? (
              <Empty title="No teams">
                Example: give the "payments" team admin on Production while they're viewers elsewhere.
              </Empty>
            ) : (
              <table className="t">
                <thead>
                  <tr>
                    <th>Team</th>
                    <th>Members</th>
                    <th>Project roles</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {data.teams.map((t) => (
                    <tr key={t.id}>
                      <td className="name">
                        {t.name} {t.require_mfa && <Badge tone="info">2FA required</Badge>}
                      </td>
                      <td className="muted">{t.members.map((m) => m.email).join(', ') || '—'}</td>
                      <td className="mono" style={{ fontSize: 12 }}>
                        {t.grants.map((g) => `${g.project}: ${g.role}`).join(' · ') || '—'}
                      </td>
                      <td className="right">
                        {canAdmin && (
                          <button className="btn ghost sm" onClick={() => setTeamEdit(t)}>
                            Edit
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Panel>
          <Panel
            title="Custom roles"
            actions={
              canAdmin && (
                <button className="btn sm" onClick={() => setRoleEdit('new')}>
                  New role
                </button>
              )
            }
          >
            {!roles ? (
              <Loading />
            ) : !roles.roles.length ? (
              <Empty title="No custom roles">
                Example: a "Deployer" role that can deploy services but only view servers and has no access to secrets.
              </Empty>
            ) : (
              <table className="t">
                <thead>
                  <tr>
                    <th>Role</th>
                    <th>Access</th>
                    <th className="right">Members</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {roles.roles.map((r) => (
                    <tr key={r.id}>
                      <td className="name">{r.name}</td>
                      <td className="muted" style={{ fontSize: 12 }}>
                        {roles.areas.map((a) => `${AREA_LABELS[a] ?? a}: ${r.permissions[a] ?? 'none'}`).join(' · ')}
                      </td>
                      <td className="right mono">{r.members}</td>
                      <td className="right">
                        {canAdmin && (
                          <button className="btn ghost sm" onClick={() => setRoleEdit(r)}>
                            Edit
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Panel>
          <Panel
            title="Audit log"
            actions={
              canAdmin && (
                <>
                  <a className="btn sm" href={`/api/v1/orgs/${org.id}/audit/export`} download>
                    Export CSV
                  </a>
                  <a className="btn sm" href={`/api/v1/orgs/${org.id}/audit/export?format=jsonl`} download>
                    Export JSON
                  </a>
                  <input
                    className="input"
                    style={{ width: 220, height: 26 }}
                    placeholder="Filter…"
                    value={q}
                    onChange={(e) => setQ(e.target.value)}
                  />
                </>
              )
            }
          >
            {!canAdmin ? (
              <div className="panel-body muted">Viewing the audit log requires the admin role.</div>
            ) : !shown ? (
              <Loading />
            ) : !shown.length ? (
              <Empty title="No audit events" />
            ) : (
              <table className="t">
                <thead>
                  <tr>
                    <th>Time</th>
                    <th>Actor</th>
                    <th>Action</th>
                    <th>Target</th>
                    <th>Project</th>
                    <th>IP</th>
                  </tr>
                </thead>
                <tbody>
                  {shown.map((r) => (
                    <tr key={r.id}>
                      <td className="mono muted nowrap">{datetime(r.created_at)}</td>
                      <td>{r.actor}</td>
                      <td className="mono">{r.action}</td>
                      <td className="mono muted">{r.target}</td>
                      <td className="muted">{r.project ?? '—'}</td>
                      <td className="mono faint">{r.ip}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Panel>
        </div>
      )}
      {inviting && <InviteDialog onClose={() => setInviting(false)} onDone={reload} />}
      {teamEdit && data && (
        <TeamDialog
          team={teamEdit === 'new' ? null : teamEdit}
          data={data}
          onClose={() => setTeamEdit(null)}
          onDone={reload}
        />
      )}
      {roleEdit && roles && (
        <RoleDialog
          role={roleEdit === 'new' ? null : roleEdit}
          areas={roles.areas}
          onClose={() => setRoleEdit(null)}
          onDone={() => {
            reloadRoles()
            reload()
            reloadMe()
          }}
        />
      )}
      {removing && (
        <Confirm
          title={removing.id === me.user.id ? `Leave ${org.name}?` : `Remove ${removing.email}?`}
          action={removing.id === me.user.id ? 'Leave' : 'Remove'}
          danger
          onClose={() => setRemoving(null)}
          onConfirm={() =>
            api('DELETE', `/orgs/${org.id}/members/${removing.id}`).then(() =>
              removing.id === me.user.id ? (window.location.href = '/') : reload(),
            )
          }
        >
          They lose access to every project in this organization immediately. Their API keys are revoked.
        </Confirm>
      )}
    </div>
  )
}
