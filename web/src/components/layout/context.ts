import { createContext, useContext } from 'react'

interface Project {
  id: string
  name: string
  role: string
}

export interface Org {
  id: string
  name: string
  role: string
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

/** Project-scoped permission: the effective role includes team grants on this project. */
export const useCan = (min: 'member' | 'admin' | 'owner') => rank[useProject().project.role] >= rank[min]

/** Organization-scoped permission (members, teams, API keys). */
export const useOrgCan = (min: 'member' | 'admin' | 'owner') => rank[useProject().org.role] >= rank[min]
