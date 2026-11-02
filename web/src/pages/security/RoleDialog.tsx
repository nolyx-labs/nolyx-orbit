import { useState } from 'react'
import { useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { Dialog } from '../../components/ui/Dialog'
import { ErrorNote } from '../../components/ui/states'
import { api } from '../../lib/api'

export interface CustomRole {
  id: string
  name: string
  permissions: Record<string, string>
  members: number
}

export const AREA_LABELS: Record<string, string> = {
  servers: 'Servers, Docker and Nomad',
  services: 'Services and deploys',
  databases: 'Databases and backups',
  monitoring: 'Monitors, alerts and status pages',
  workflows: 'Workflows',
  secrets: 'Secrets',
  updates: 'OS updates and reboots',
}

export function RoleDialog({
  role,
  areas,
  onClose,
  onDone,
}: {
  role: CustomRole | null
  areas: string[]
  onClose: () => void
  onDone: () => void
}) {
  const { org } = useProject()
  const [name, setName] = useState(role?.name ?? '')
  const [perms, setPerms] = useState<Record<string, string>>(
    role?.permissions ?? Object.fromEntries(areas.map((a) => [a, 'viewer'])),
  )
  const [deleting, setDeleting] = useState(false)
  const [error, setError] = useState<string>()
  const save = () =>
    api(role ? 'PUT' : 'POST', role ? `/orgs/${org.id}/roles/${role.id}` : `/orgs/${org.id}/roles`, {
      name,
      permissions: perms,
    }).then(
      () => {
        onDone()
        onClose()
      },
      (e) => setError(e.message),
    )
  return (
    <Dialog
      title={role ? `Edit ${role.name}` : 'New custom role'}
      onClose={onClose}
      footer={
        <>
          {role && (
            <button className="btn danger" style={{ marginRight: 'auto' }} onClick={() => setDeleting(true)}>
              Delete
            </button>
          )}
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" onClick={save}>
            {role ? 'Save' : 'Create role'}
          </button>
        </>
      }
    >
      <div className="muted">
        A custom role narrows a member's organization role in each area of every project. It never grants more than
        their role, and team grants still apply on top.
      </div>
      <label className="field">
        <span>Name</span>
        <input
          className="input"
          placeholder="Deployer"
          value={name}
          onChange={(e) => setName(e.target.value)}
          autoFocus
        />
      </label>
      <div className="grid-2" style={{ gridTemplateColumns: '1fr 140px', alignItems: 'center' }}>
        {areas.map((a) => (
          <label key={a} style={{ display: 'contents' }}>
            <span>{AREA_LABELS[a] ?? a}</span>
            <select
              className="select"
              value={perms[a] ?? 'none'}
              onChange={(e) => setPerms({ ...perms, [a]: e.target.value })}
            >
              {['none', 'viewer', 'member', 'admin'].map((l) => (
                <option key={l}>{l}</option>
              ))}
            </select>
          </label>
        ))}
      </div>
      <ErrorNote error={error} />
      {deleting && role && (
        <Confirm
          title={`Delete ${role.name}?`}
          action="Delete"
          danger
          onClose={() => setDeleting(false)}
          onConfirm={() =>
            api('DELETE', `/orgs/${org.id}/roles/${role.id}`).then(() => {
              onDone()
              onClose()
            })
          }
        >
          {role.members
            ? `${role.members} member${role.members > 1 ? 's' : ''} go back to their full organization role.`
            : 'No members have this role.'}
        </Confirm>
      )}
    </Dialog>
  )
}
