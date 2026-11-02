import { useProject } from '../../components/layout/context'
import { useApi } from '../../lib/api'

export interface OrgData {
  org: { id: string; name: string; require_mfa: boolean; require_sso: boolean; audit_retention_days: number }
  role: string
  members: {
    id: string
    email: string
    name: string
    role: string
    custom_role_id: string | null
    mfa_enabled: boolean
    teams: string[]
  }[]
  invites: { id: string; email: string; role: string; expires_at: string; invited_by: string }[]
  teams: {
    id: string
    name: string
    require_mfa: boolean
    members: { id: string; email: string }[]
    grants: { project_id: string; project: string; role: string }[]
  }[]
  projects: { id: string; name: string; created_at: string; servers: number; apps: number; databases: number }[]
}

export function useOrg() {
  const { org } = useProject()
  return useApi<OrgData>(`/orgs/${org.id}`)
}
