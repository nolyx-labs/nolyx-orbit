// ---------- types ----------

export interface Server {
  id: string
  name: string
  hostname: string
  ip: string
  os: string
  arch: string
  kernel: string
  cpu_count: number
  mem_total: number
  disk_total: number
  agent_version: string
  docker_version: string
  status: 'online' | 'offline'
  last_seen_at: string | null
  created_at: string
  cpu: number | null
  mem_used: number | null
  disk_used: number | null
  net_rx: number | null
  net_tx: number | null
  load1: number | null
  uptime: number | null
  containers_running: number
  nomad_version: string
  region: string
  provider: string
  io_read: number | null
  io_write: number | null
  io_util: number | null
  environment: 'production' | 'staging' | 'development'
  groups: { id: string; name: string }[]
  security: { high: number; medium: number; low: number; scanned_at: string | null }
}

export interface Container {
  id: string
  server_id: string
  server_name: string
  name: string
  image: string
  state: string
  status: string
  health: string
  created: string
  ports: string[]
  labels: Record<string, string>
  restart_count: number
  cpu: number
  mem_used: number
  mem_limit: number
  updated_at: string
}

export interface Series {
  t: number[]
  [k: string]: number[]
}

export interface Deployment {
  id: string
  number: number
  status: 'running' | 'succeeded' | 'failed'
  image: string
  error: string | null
  created_at: string
  finished_at: string | null
  duration: number
  app_id: string
  app: string
  server_name: string | null
  triggered_by: string | null
  commit_sha: string
  commit_message: string
  commit_author: string
  branch: string
  orchestrator: string
}

export interface OrbitEvent {
  id: number
  severity: 'info' | 'warning' | 'error'
  message: string
  created_at: string
  server_id: string | null
  server_name?: string
}

export interface Database {
  id: string
  name: string
  host: string
  port: number
  username: string
  dbname: string
  sslmode: string
  managed: boolean
  version: string
  role: 'primary' | 'replica' | 'unknown'
  status: 'healthy' | 'degraded' | 'down' | 'unknown' | 'provisioning' | 'failed'
  system_id: string
  error: string
  last_seen_at: string | null
  created_at: string
  primary_id: string | null
  server_id: string
  server_name: string
  server_ip: string
  server_status: string
  replication_ready: boolean
  server_usage: { cpu: number; mem: number; disk: number; io_util: number } | null
  stats: {
    connections?: number
    max_connections?: number
    tps?: number | null
    qps?: number | null
    cache_hit?: number | null
    size?: number
    waiting_locks?: number
    wal_position?: string
    lag_ms?: number | null
    replay_rate?: number | null
    receiver?: { status: string; sender_host: string; sender_port: number } | null
    replicas?: {
      name: string
      client_addr: string
      state: string
      sync_state: string
      replay_lsn: string
      lag_bytes: number
      replay_lag_ms: number | null
    }[]
    slow_queries?: { pid: number; seconds: number; state: string; query: string; wait: string }[]
    tables?: { name: string; bytes: number; rows: number }[]
    issues?: string[]
  }
  backup_config?: {
    interval_hours: number
    retention: number
    enabled: boolean
    s3: boolean
    last_run_at: string | null
  } | null
}

export interface Operation {
  id: string
  kind: string
  target: string
  status: 'running' | 'succeeded' | 'failed'
  plan: string[]
  steps: Record<string, string>
  log: string
  error: string | null
  output: any
  created_at: string
  finished_at: string | null
  duration: number
  database_id: string | null
  server_id: string | null
  server_name: string | null
  triggered_by: string | null
}
