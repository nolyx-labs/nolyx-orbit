// Workflow definition types, labels and templates. Mirrors internal/server/workflow_def.go.

export interface WfTrigger {
  type: string
  every?: string
  at?: string
  weekday?: number
  service_id?: string
  monitor_id?: string
  server_id?: string
  branch?: string
  status?: string
}

export interface WfCond {
  type: string
  field?: string
  value?: string
  monitor_id?: string
  service_id?: string
  server_id?: string
  percent?: number
  from?: string
  to?: string
}

export interface WfStep {
  type: string
  service_id?: string
  monitor_id?: string
  command?: string
  seconds?: number
  method?: string
  url?: string
  headers?: Record<string, string>
  body?: string
  channel_ids?: string[]
  user_ids?: string[]
  message?: string
  if?: WfCond
  then?: WfStep[]
  else?: WfStep[]
  on_failure?: WfStep[]
}

export interface WfDef {
  trigger: WfTrigger
  steps: WfStep[]
}

export interface Workflow {
  id: string
  name: string
  enabled: boolean
  definition: WfDef
  updated_at: string
  saved_by: string | null
  webhook_url: string | null
}

export interface WorkflowSummary {
  id: string
  name: string
  enabled: boolean
  trigger: WfTrigger
  last_run: { id: string; status: string; error: string; created_at: string } | null
}

export interface Run {
  id: string
  status: 'running' | 'succeeded' | 'failed' | 'skipped'
  trigger: string
  error: string
  created_at: string
  finished_at: string | null
  started_by: string | null
  duration: number
}

export interface RunStep {
  id: number
  path: string
  type: string
  title: string
  status: 'running' | 'succeeded' | 'failed'
  branch: string
  output: string
  error: string
  started_at: string
  duration: number
}

/** [type, label, scope fields it accepts] */
export const TRIGGERS: [string, string, string[]][] = [
  ['manual', 'Run by hand', []],
  ['schedule', 'On a schedule', []],
  ['webhook', 'Webhook', []],
  ['github_push', 'Push to GitHub', ['service_id', 'branch']],
  ['deploy_finished', 'A deploy finishes', ['service_id', 'status']],
  ['service_stopped', 'A service stops', ['service_id']],
  ['service_restarting', 'A service keeps restarting', ['service_id']],
  ['monitor_down', 'A monitor goes down', ['monitor_id']],
  ['monitor_up', 'A monitor is back up', ['monitor_id']],
  ['server_offline', 'A server stops reporting', ['server_id']],
  ['disk_full', 'A disk is nearly full', ['server_id']],
  ['backup_failed', 'A backup fails', []],
]

/** [type, label, can fail] */
export const STEPS: [string, string, boolean][] = [
  ['redeploy', 'Redeploy a service', true],
  ['start', 'Start a service', true],
  ['stop', 'Stop a service', true],
  ['restart', 'Restart a service', true],
  ['run_command', 'Run a command in a service', true],
  ['check_monitor', 'Check a monitor', true],
  ['http', 'HTTP request', true],
  ['alert', 'Send an alert', true],
  ['wait', 'Wait', false],
  ['if', 'If', false],
]

export const CONDITIONS: [string, string][] = [
  ['field_equals', 'A trigger field equals'],
  ['field_contains', 'A trigger field contains'],
  ['monitor_up', 'A monitor is up'],
  ['service_running', 'A service is running'],
  ['disk_below', 'A disk is below'],
  ['time_between', 'The time (UTC) is between'],
]

export const WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']

export const canFail = (type: string) => STEPS.find((s) => s[0] === type)?.[2] ?? false

type Names = Record<string, string>

export function triggerSummary(t: WfTrigger, names: Names = {}): string {
  const label = TRIGGERS.find((x) => x[0] === t.type)?.[1] ?? t.type
  if (t.type === 'schedule') {
    const at = t.at ?? '00:00'
    if (t.every === 'hourly') return `Every hour at :${at.slice(3)}`
    if (t.every === 'weekly') return `Every ${WEEKDAYS[t.weekday ?? 0]} at ${at} UTC`
    return `Every day at ${at} UTC`
  }
  const scope = [t.service_id, t.monitor_id, t.server_id].filter(Boolean).map((id) => names[id!] ?? 'a deleted item')
  if (t.branch) scope.push(`branch ${t.branch}`)
  if (t.status) scope.push(t.status === 'succeeded' ? 'only successes' : 'only failures')
  return scope.length ? `${label} (${scope.join(', ')})` : label
}

export function stepSummary(s: WfStep, names: Names = {}): string {
  const n = (id?: string) => (id ? (names[id] ?? 'a deleted item') : 'nothing chosen')
  switch (s.type) {
    case 'redeploy':
    case 'start':
    case 'stop':
    case 'restart':
      return `${s.type[0].toUpperCase() + s.type.slice(1)} ${n(s.service_id)}`
    case 'run_command':
      return `Run \`${s.command || '…'}\` in ${n(s.service_id)}`
    case 'check_monitor':
      return `Check ${n(s.monitor_id)} until it passes`
    case 'http':
      return `${s.method ?? 'POST'} ${s.url || '…'}`
    case 'alert':
      return `Send an alert${s.message ? `: ${s.message}` : ''}`
    case 'wait':
      return `Wait ${duration(s.seconds ?? 0)}`
    case 'if':
      return `If ${condSummary(s.if, names)}`
  }
  return s.type
}

function condSummary(c: WfCond | undefined, names: Names): string {
  if (!c) return '…'
  switch (c.type) {
    case 'field_equals':
      return `${c.field || '…'} is "${c.value ?? ''}"`
    case 'field_contains':
      return `${c.field || '…'} contains "${c.value ?? ''}"`
    case 'monitor_up':
      return `${names[c.monitor_id ?? ''] ?? 'a monitor'} is up`
    case 'service_running':
      return `${names[c.service_id ?? ''] ?? 'a service'} is running`
    case 'disk_below':
      return `the disk on ${names[c.server_id ?? ''] ?? 'a server'} is below ${c.percent ?? 0}%`
    case 'time_between':
      return `the time is between ${c.from} and ${c.to} UTC`
  }
  return c.type
}

function duration(sec: number): string {
  if (sec % 3600 === 0 && sec >= 3600) return `${sec / 3600} hour${sec === 3600 ? '' : 's'}`
  if (sec % 60 === 0 && sec >= 60) return `${sec / 60} minute${sec === 60 ? '' : 's'}`
  return `${sec} second${sec === 1 ? '' : 's'}`
}

export function newStep(type: string, first: { service?: string; monitor?: string }): WfStep {
  switch (type) {
    case 'run_command':
      return { type, service_id: first.service, command: '' }
    case 'check_monitor':
      return { type, monitor_id: first.monitor, seconds: 120 }
    case 'http':
      return { type, method: 'POST', url: 'https://', body: '' }
    case 'alert':
      return { type, channel_ids: [], user_ids: [], message: '' }
    case 'wait':
      return { type, seconds: 60 }
    case 'if':
      return { type, if: { type: 'field_equals', field: 'data.status', value: '' }, then: [], else: [] }
  }
  return { type, service_id: first.service }
}

export interface Template {
  key: string
  name: string
  description: string
  build: (first: { service?: string; monitor?: string; to: Pick<WfStep, 'channel_ids' | 'user_ids'> }) => WfDef
}

const chatBody =
  '{"text": "{{service}}: {{trigger}} ({{run_url}})", "content": "{{service}}: {{trigger}} ({{run_url}})"}'

export const TEMPLATES: Template[] = [
  {
    key: 'website',
    name: 'Keep my website online',
    description: 'When a monitor goes down, restart the service, wait, check again and tell you how it went.',
    build: (f) => ({
      trigger: { type: 'monitor_down', monitor_id: f.monitor },
      steps: [
        { type: 'restart', service_id: f.service },
        { type: 'wait', seconds: 30 },
        {
          type: 'check_monitor',
          monitor_id: f.monitor,
          seconds: 120,
          on_failure: [{ type: 'alert', ...f.to, message: '{{monitor}} is still down after a restart: {{run_url}}' }],
        },
        {
          type: 'if',
          if: { type: 'monitor_up', monitor_id: f.monitor },
          then: [{ type: 'alert', ...f.to, message: '{{monitor}} went down and is back up after a restart' }],
          else: [],
        },
      ],
    }),
  },
  {
    key: 'crash',
    name: 'Restart apps that crash',
    description: 'When a service stops on its own, start it again and alert only if it does not stay up.',
    build: (f) => ({
      trigger: { type: 'service_stopped', service_id: f.service },
      steps: [
        { type: 'start', service_id: f.service },
        { type: 'wait', seconds: 30 },
        {
          type: 'if',
          if: { type: 'service_running', service_id: f.service },
          then: [],
          else: [{ type: 'alert', ...f.to, message: '{{service}} stopped and did not come back up' }],
        },
      ],
    }),
  },
  {
    key: 'ci',
    name: 'Deploy when CI passes',
    description: 'Your CI calls a secret URL; Orbit redeploys, checks the site and posts to a chat webhook.',
    build: (f) => ({
      trigger: { type: 'webhook' },
      steps: [
        { type: 'redeploy', service_id: f.service },
        { type: 'check_monitor', monitor_id: f.monitor, seconds: 300 },
        { type: 'http', method: 'POST', url: 'https://hooks.slack.com/services/…', body: chatBody },
      ],
    }),
  },
  {
    key: 'chat',
    name: 'Post deploys to Slack or Discord',
    description: 'When a deploy finishes, post successes to a chat webhook and alert on failures.',
    build: (f) => ({
      trigger: { type: 'deploy_finished' },
      steps: [
        {
          type: 'if',
          if: { type: 'field_equals', field: 'data.status', value: 'succeeded' },
          then: [
            {
              type: 'http',
              method: 'POST',
              url: 'https://hooks.slack.com/services/…',
              body: '{"text": "Deployed {{service}} #{{data.number}}", "content": "Deployed {{service}} #{{data.number}}"}',
            },
          ],
          else: [{ type: 'alert', ...f.to, message: 'Deploy #{{data.number}} of {{service}} failed' }],
        },
      ],
    }),
  },
  {
    key: 'pagerduty',
    name: 'Page on-call when a server goes down',
    description: 'When a server stops reporting, open a PagerDuty incident through the Events API.',
    build: () => ({
      trigger: { type: 'server_offline' },
      steps: [
        {
          type: 'http',
          method: 'POST',
          url: 'https://events.pagerduty.com/v2/enqueue',
          body: JSON.stringify({
            routing_key: 'YOUR_ROUTING_KEY',
            event_action: 'trigger',
            dedup_key: 'orbit-{{server}}',
            payload: {
              summary: 'Server {{server}} stopped reporting to Orbit',
              source: '{{server}}',
              severity: 'critical',
            },
            links: [{ href: '{{run_url}}', text: 'Orbit workflow run' }],
          }),
        },
      ],
    }),
  },
  {
    key: 'scratch',
    name: 'Start from scratch',
    description: 'A blank workflow you run by hand. Pick a trigger and add steps.',
    build: () => ({ trigger: { type: 'manual' }, steps: [] }),
  },
]

/** Reads or replaces the node at a path like ['steps', 0, 'then', 1] in a copy of the definition. */
export function getAt(def: WfDef, path: (string | number)[]): any {
  return path.reduce((n: any, k) => n?.[k], def)
}

export function setAt(def: WfDef, path: (string | number)[], value: unknown): WfDef {
  const copy = structuredClone(def)
  const parent = getAt(copy, path.slice(0, -1))
  const key = path[path.length - 1]
  if (value === undefined && Array.isArray(parent)) parent.splice(key as number, 1)
  else parent[key] = value
  return copy
}

/** Inserts into the list at path (creating it), at index. */
export function insertAt(def: WfDef, list: (string | number)[], index: number, step: WfStep): WfDef {
  const copy = structuredClone(def)
  const parent = getAt(copy, list.slice(0, -1))
  const key = list[list.length - 1]
  parent[key] = parent[key] ?? []
  parent[key].splice(index, 0, step)
  return copy
}
