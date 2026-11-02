export interface Rule {
  id: string
  name: string
  metric: string
  threshold: number | null
  duration_minutes: number
  has_webhook: boolean
  channel_ids: string[]
  firing: number
}

export interface Incident {
  id: number
  rule: string
  metric: string
  value: number
  threshold: number
  target_type: string
  target_id: string
  target_name: string
  server_id: string | null
  opened_at: string
  resolved_at: string | null
  acknowledged_at: string | null
  acknowledged_by: string | null
}

export interface Channel {
  id: string
  name: string
  type: string
}

export const METRICS: Record<string, { label: string; unit: '%' | 's' | ''; target: string; hint: string }> = {
  cpu: { label: 'CPU', unit: '%', target: 'server', hint: 'Average CPU over the window' },
  memory: { label: 'Memory', unit: '%', target: 'server', hint: 'Average memory used over the window' },
  disk: { label: 'Disk usage', unit: '%', target: 'server', hint: 'Root filesystem used' },
  io: { label: 'Disk busy', unit: '%', target: 'server', hint: 'Time the busiest disk spends on I/O' },
  offline: { label: 'Server offline', unit: '', target: 'server', hint: 'No heartbeat for the whole window' },
  replication_lag: {
    label: 'Replication lag',
    unit: 's',
    target: 'database',
    hint: 'Average replay delay of each replica',
  },
  db_connections: {
    label: 'DB connections',
    unit: '%',
    target: 'database',
    hint: 'Connections as a share of max_connections',
  },
  db_down: {
    label: 'Database down',
    unit: '',
    target: 'database',
    hint: 'The agent cannot connect for the whole window',
  },
  error_rate: {
    label: 'Error rate',
    unit: '%',
    target: 'service',
    hint: '5xx share of requests (zero-downtime services, min. 20 requests)',
  },
  container_restarts: {
    label: 'Container unhealthy',
    unit: '',
    target: 'container',
    hint: 'Restarting, dead or failing its health check',
  },
  deployment_failed: {
    label: 'Deployment failed',
    unit: '',
    target: 'service',
    hint: 'Fires on a failed deploy, resolves on the next success',
  },
  monitor_down: {
    label: 'Monitor down',
    unit: '',
    target: 'monitor',
    hint: 'Two failed checks in a row; resolves on the next success',
  },
}

/** Metrics that fire on an event, so the rule has no duration. */
export const TIMELESS = ['deployment_failed', 'container_restarts', 'monitor_down']
