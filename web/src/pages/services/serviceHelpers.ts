import { useProject } from '../../components/layout/context'
import { Tone } from '../../components/ui/status'
import { useApi } from '../../lib/api'
import { NomadCluster } from '../nomad/nomadHelpers'

export interface App {
  id: string
  name: string
  server_id: string | null
  server_name: string | null
  image: string
  ports: string[]
  env_keys: string[]
  health_path: string
  created_at: string
  proxy: boolean
  cpus: number
  memory_mb: number
  orchestrator: 'docker' | 'nomad'
  nomad_cluster_id: string | null
  instances: number
  repo_url: string
  branch: string
  dockerfile: string
  build_context: string
  has_repo_token: boolean
  auto_deploy: boolean
  webhook_url: string | null
  webhook_secret?: string | null
  carried: {
    mounts?: { type: string; source?: string; target: string; read_only?: boolean }[]
    networks?: { name: string }[]
  }
  importing: boolean
  container: { id: string; state: string; status: string; health: string; cpu: number; mem_used: number } | null
  last_deployment: { id: string; number: number; status: string; created_at: string; commit_sha: string } | null
}

export function useNomadAllocs(app: App | undefined) {
  const { project } = useProject()
  const [cs] = useApi<NomadCluster[]>(app?.orchestrator === 'nomad' ? `/projects/${project.id}/nomad/clusters` : null)
  const c = cs?.find((c) => c.id === app?.nomad_cluster_id)
  return c?.allocations.filter((a) => a.JobID === app?.name && a.DesiredStatus === 'run') ?? []
}

export function appStatus(a: App, allocs: any[] = []): [Tone, string] {
  if (a.orchestrator === 'nomad') {
    const running = allocs.filter((x) => x.ClientStatus === 'running').length
    if (!allocs.length) return a.last_deployment?.status === 'running' ? ['info', 'deploying'] : ['', 'not deployed']
    return running >= a.instances
      ? ['ok', `${running}/${a.instances} healthy`]
      : ['warn', `${running}/${a.instances} running`]
  }
  if (!a.container) return a.last_deployment?.status === 'running' ? ['info', 'deploying'] : ['', 'not deployed']
  if (a.container.health === 'unhealthy') return ['err', 'unhealthy']
  if (a.container.state !== 'running') return ['err', a.container.state]
  return ['ok', 'healthy']
}

export function parseEnv(text: string): [Record<string, string>, string | undefined] {
  const env: Record<string, string> = {}
  for (const [i, raw] of text.split('\n').entries()) {
    const line = raw.trim()
    if (!line || line.startsWith('#')) continue
    const eq = line.indexOf('=')
    if (eq < 1) return [env, `Line ${i + 1}: expected KEY=value`]
    env[line.slice(0, eq).trim()] = line.slice(eq + 1)
  }
  return [env, undefined]
}
