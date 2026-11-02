import { useState } from 'react'
import { useProject } from '../../components/layout/context'
import { api, useApi } from '../../lib/api'
import { ago } from '../../lib/format'
import { useLive } from '../../lib/live'

export interface SystemdService {
  unit: string
  description: string
  load: string
  active: string
  sub: string
  enabled: string
  memory: number
}

export interface ListenPort {
  proto: 'tcp' | 'udp'
  address: string
  port: number
  scope: 'all' | 'localhost' | 'specific'
  pid?: number
  process?: string
  container?: string
}

export interface FirewallRule {
  port: string
  proto: string
  action: string
  from: string
  v6?: boolean
  raw: string
}

export interface Finding {
  key: string
  severity: 'high' | 'medium' | 'low'
  title: string
  detail: string
  fix: string
}

export interface Insight {
  inventory: {
    ts: string
    services: SystemdService[]
    compose: { name: string; working_dir: string; services: { service: string; container: string; state: string }[] }[]
    ports: ListenPort[]
    specs: {
      cpu_model: string
      disks: { mount: string; device: string; fs: string; size: number; used: number }[] | null
      interfaces: { name: string; mac: string; addrs: string[]; up: boolean }[] | null
    }
    errors?: string[]
  } | null
  inventory_at: string | null
  log_units: string[]
  scan: {
    container: boolean
    firewall: { tool: string; active: boolean; incoming: string; outgoing: string; rules: FirewallRule[] | null }
    ssh: { installed: boolean; permit_root_login: string; password_authentication: string; ports: number[] | null }
    auto_updates: string
    reboot_required: boolean
    security_updates: number
    errors?: string[]
    findings: Finding[]
  } | null
  scan_at: string | null
}

/** The server's latest inventory and scan, refreshed live. */
export function useInsight(sid: string) {
  const { project } = useProject()
  const [data, error, reload] = useApi<Insight>(`/projects/${project.id}/servers/${sid}/insight`)
  useLive(project.id, ['insight'], reload)
  return [data, error, reload] as const
}

/** "Updated 3m ago · Refresh": asks the agent for a fresh inventory. */
export function RefreshInventory({ sid, at, onDone }: { sid: string; at: string | null; onDone: () => void }) {
  const { project } = useProject()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  return (
    <span className="row" style={{ gap: 10 }}>
      {error ? <span className="faint">{error}</span> : <span className="faint">Updated {ago(at)}</span>}
      <button
        className="btn sm"
        disabled={busy}
        onClick={() => {
          setBusy(true)
          setError(undefined)
          api('POST', `/projects/${project.id}/servers/${sid}/insight/refresh`)
            .then(onDone, (e) => setError(e.message))
            .finally(() => setBusy(false))
        }}
      >
        {busy ? 'Refreshing…' : 'Refresh'}
      </button>
    </span>
  )
}
