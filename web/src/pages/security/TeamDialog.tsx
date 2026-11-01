import { useState } from 'react'
import { useProject } from '../../components/layout/context'
import { Dialog } from '../../components/ui/Dialog'
import { ErrorNote } from '../../components/ui/states'
import { api } from '../../lib/api'
import { OrgData } from '../settings/useOrg'

export function TeamDialog({
  team,
  data,
  onClose,
  onDone,
}: {
  team: OrgData['teams'][number] | null
  data: OrgData
  onClose: () => void
  onDone: () => void
}) {
  const { org } = useProject()
  const [name, setName] = useState(team?.name ?? '')
  const [members, setMembers] = useState<string[]>(team?.members.map((m) => m.id) ?? [])
  const [grants, setGrants] = useState<Record<string, string>>(
    Object.fromEntries(team?.grants.map((g) => [g.project_id, g.role]) ?? []),
  )
  const [error, setError] = useState<string>()
  const save = async () => {
    try {
      let id = team?.id
      if (!id) id = (await api<{ id: string }>('POST', `/orgs/${org.id}/teams`, { name })).id
      await api('PUT', `/orgs/${org.id}/teams/${id}`, {
        user_ids: members,
        grants: Object.entries(grants)
          .filter(([, r]) => r)
          .map(([project_id, role]) => ({ project_id, role })),
      })
      onDone()
      onClose()
    } catch (e: any) {
      setError(e.message)
    }
  }
  return (
    <Dialog
      title={team ? `Edit ${team.name}` : 'New team'}
      onClose={onClose}
      wide
      footer={
        <>
          {team && (
            <button
              className="btn danger"
              style={{ marginRight: 'auto' }}
              onClick={() =>
                api('DELETE', `/orgs/${org.id}/teams/${team.id}`).then(() => {
                  onDone()
                  onClose()
                })
              }
            >
              Delete team
            </button>
          )}
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" disabled={!name} onClick={save}>
            Save
          </button>
        </>
      }
    >
      {!team && (
        <label className="field">
          <span>Name</span>
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} autoFocus />
        </label>
      )}
      <div className="field">
        <span>Members</span>
        <div className="checkbox-row">
          {data.members.map((m) => (
            <label key={m.id}>
              <input
                type="checkbox"
                checked={members.includes(m.id)}
                onChange={(e) => setMembers(e.target.checked ? [...members, m.id] : members.filter((x) => x !== m.id))}
              />
              {m.email}
            </label>
          ))}
        </div>
      </div>
      <div className="field">
        <span>Project roles</span>
        <table className="t">
          <tbody>
            {data.projects.map((p) => (
              <tr key={p.id}>
                <td>{p.name}</td>
                <td className="right">
                  <select
                    className="select"
                    style={{ width: 160 }}
                    value={grants[p.id] ?? ''}
                    onChange={(e) => setGrants({ ...grants, [p.id]: e.target.value })}
                  >
                    <option value="">organization role</option>
                    <option value="viewer">viewer</option>
                    <option value="member">member</option>
                    <option value="admin">admin</option>
                  </select>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <span className="hint">A grant only raises access. A member's organization role is the floor.</span>
      </div>
      <ErrorNote error={error} />
    </Dialog>
  )
}
