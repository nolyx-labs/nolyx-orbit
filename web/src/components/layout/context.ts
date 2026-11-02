import { createContext, useContext } from 'react'
import { useLocation } from 'react-router'

interface Project {
  id: string
  name: string
  role: string
  /** The highest role my teams grant on this project, or -1. */
  team_rank: number
}

export interface Org {
  id: string
  name: string
  role: string
  /** The organization-wide setting. */
  require_mfa: boolean
  /** The org or one of my teams in it requires 2FA and I haven't turned it on: everything but my account is locked. */
  mfa_blocked: boolean
  /** My custom role's level per area (none, viewer, member, admin), or null without one. */
  permissions: Record<string, string> | null
  projects: Project[]
}

export interface Me {
  user: { id: string; email: string; name: string; mfa_enabled: boolean }
  orgs: Org[]
}

interface Ctx {
  me: Me
  org: Org
  project: Project
  reloadMe: () => void
}

export const ProjectCtx = createContext<Ctx>(null!)

export const useProject = () => useContext(ProjectCtx)

const rank = { viewer: 0, member: 1, admin: 2, owner: 3 } as Record<string, number>

// Page → custom-role area, mirroring areaOf in internal/server/roles.go.
const AREAS: Record<string, string> = {
  servers: 'servers',
  clusters: 'servers',
  docker: 'servers',
  nomad: 'servers',
  'settings/agents': 'servers',
  services: 'services',
  deployments: 'services',
  releases: 'services',
  catalog: 'services',
  databases: 'databases',
  replicas: 'databases',
  backups: 'databases',
  monitors: 'monitoring',
  'status-pages': 'monitoring',
  alerts: 'monitoring',
  workflows: 'workflows',
  secrets: 'secrets',
  updates: 'updates',
}

/** The custom-role area of a project page path such as /p/{pid}/servers/{sid}/updates, or ''. */
export const pageArea = (path: string) => {
  const [, , , seg, id, tab] = path.split('/')
  if (seg === 'servers' && tab === 'updates') return 'updates'
  return AREAS[`${seg}/${id}`] ?? AREAS[seg] ?? ''
}

/** Project-scoped permission on the current page: the org role capped by my custom role, raised by team grants. */
export const useCan = (min: 'member' | 'admin' | 'owner') => {
  const { org, project } = useProject()
  const area = pageArea(useLocation().pathname)
  let r = rank[project.role]
  if (org.permissions && area && org.role !== 'owner') {
    r = Math.max(Math.min(rank[org.role], rank[org.permissions[area]] ?? -1), project.team_rank)
  }
  return r >= rank[min]
}

/** Organization-scoped permission (members, teams, API keys). */
export const useOrgCan = (min: 'member' | 'admin' | 'owner') => rank[useProject().org.role] >= rank[min]
