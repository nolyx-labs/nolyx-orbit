import { useProject } from '../../components/layout/context'
import { Tone } from '../../components/ui/status'
import { useApi } from '../../lib/api'
import { useLive } from '../../lib/live'
import { Server } from '../../lib/types'

export interface NomadCluster {
  id: string
  region: string
  datacenter: string
  leader: string
  peers: string[]
  updated_at: string
  fresh: boolean
  reporters: string[]
  nodes: any[]
  jobs: any[]
  allocations: any[]
  deployments: any[]
}

export const statusTone = (s: string): Tone =>
  ['ready', 'running', 'successful', 'complete'].includes(s)
    ? 'ok'
    : ['pending', 'initializing'].includes(s)
      ? 'info'
      : s === 'dead' || s === 'failed' || s === 'down' || s === 'lost'
        ? 'err'
        : 'warn'

export function useClusters() {
  const { project } = useProject()
  const [cs, err, reload] = useApi<NomadCluster[]>(`/projects/${project.id}/nomad/clusters`)
  useLive(project.id, ['nomad'], reload, 5000)
  return [cs, err] as const
}

/** Matches a Nomad node to an Orbit server by address or hostname; a one-node cluster maps to its only reporter. */
export function serverFor(node: any, c: NomadCluster, servers?: Server[]) {
  return (
    servers?.find((s) => s.ip === node.Address || s.hostname === node.Name || s.name === node.Name) ??
    (c.nodes.length === 1 && c.reporters.length === 1 ? servers?.find((s) => s.id === c.reporters[0]) : undefined)
  )
}
