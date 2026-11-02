import { Tone } from '../../components/ui/status'

export interface Component {
  id: string
  name: string
  description: string
  group_name: string
  position: number
  source_type: 'manual' | 'monitor' | 'server' | 'app'
  source_id: string | null
  manual_status: ComponentStatus
  status: ComponentStatus
}

export interface Incident {
  id: string
  kind: 'incident' | 'maintenance'
  name: string
  impact: 'none' | 'minor' | 'major' | 'critical' | 'maintenance'
  status: string
  scheduled_for: string | null
  scheduled_until: string | null
  auto_transition: boolean
  created_at: string
  resolved_at: string | null
  components: Record<string, ComponentStatus>
  updates: { id: number; status: string; message: string; created_at: string; author: string | null }[]
}

export interface StatusPage {
  id: string
  name: string
  slug: string
  domain: string | null
  public: boolean
  description: string
  support_url: string
  brand_color: string
  has_logo: boolean
  url: string
  components: Component[]
  incidents: Incident[]
  subscribers: number
}

export type ComponentStatus =
  'operational' | 'degraded_performance' | 'partial_outage' | 'major_outage' | 'under_maintenance'

export const COMPONENT_STATUSES: [ComponentStatus, string, Tone][] = [
  ['operational', 'Operational', 'ok'],
  ['degraded_performance', 'Degraded performance', 'warn'],
  ['partial_outage', 'Partial outage', 'warn'],
  ['major_outage', 'Major outage', 'err'],
  ['under_maintenance', 'Under maintenance', 'info'],
]

export const componentLabel = (s: string) => COMPONENT_STATUSES.find((x) => x[0] === s)?.[1] ?? s
export const componentTone = (s: string): Tone => COMPONENT_STATUSES.find((x) => x[0] === s)?.[2] ?? ''

export const INCIDENT_STATUSES = {
  incident: [
    ['investigating', 'Investigating'],
    ['identified', 'Identified'],
    ['monitoring', 'Monitoring'],
    ['resolved', 'Resolved'],
  ],
  maintenance: [
    ['scheduled', 'Scheduled'],
    ['in_progress', 'In progress'],
    ['verifying', 'Verifying'],
    ['completed', 'Completed'],
  ],
} as const

export const statusLabel = (s: string) =>
  [...INCIDENT_STATUSES.incident, ...INCIDENT_STATUSES.maintenance].find((x) => x[0] === s)?.[1] ?? s

export const IMPACTS = [
  ['none', 'None', ''],
  ['minor', 'Minor', 'warn'],
  ['major', 'Major', 'warn'],
  ['critical', 'Critical', 'err'],
] as const

export const impactTone = (i: string): Tone =>
  i === 'critical' ? 'err' : i === 'maintenance' ? 'info' : i === 'none' ? '' : 'warn'

/** <input type="datetime-local"> works in local time without a zone. */
export const toLocalInput = (d: Date) =>
  new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16)
