import { useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { useCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { Dialog } from '../../components/ui/Dialog'
import { Panel } from '../../components/ui/Panel'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { StatusText } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { ago, bytes, datetime } from '../../lib/format'
import { useLive } from '../../lib/live'
import { opPath } from '../../lib/paths'
import { Server } from '../../lib/types'

interface BackupConfig {
  schedule: 'daily' | 'weekly' | 'manual'
  destination: 's3' | 'server'
  s3_provider: string
  s3?: { bucket: string; prefix: string; region: string; endpoint: string }
  target_server_id: string | null
  target_name: string | null
  target_addr: string
  last_run_at: string | null
}

interface Archive {
  id: string
  service: string
  kind: string
  status: 'succeeded' | 'failed'
  file: string
  location: string
  size: number
  duration_ms: number
  error: string
  created_at: string
  operation_id: string | null
}

interface ServerBackupData {
  config: BackupConfig | null
  backups: Archive[]
  runs: { id: string; kind: string; target: string; status: string; error: string; created_at: string }[]
}

// Mirrors s3Providers in internal/server/server_backups.go.
const PROVIDERS: [string, string][] = [
  ['aws', 'Amazon S3'],
  ['r2', 'Cloudflare R2'],
  ['b2', 'Backblaze B2'],
  ['hetzner', 'Hetzner Object Storage'],
  ['digitalocean', 'DigitalOcean Spaces'],
  ['wasabi', 'Wasabi'],
  ['other', 'Other S3-compatible'],
]
const providerName = (p: string) => PROVIDERS.find((x) => x[0] === p)?.[1] ?? p

export function ServerBackups({ s }: { s: Server }) {
  const { project } = useProject()
  const nav = useNavigate()
  const canRun = useCan('member')
  const canAdmin = useCan('admin')
  const [data, loadError, reload] = useApi<ServerBackupData>(`/projects/${project.id}/servers/${s.id}/backups`)
  useLive(project.id, ['server-backups', 'operations'], reload)
  const [configuring, setConfiguring] = useState(false)
  const [restoring, setRestoring] = useState<Archive>()
  const [error, setError] = useState<string>()
  if (loadError) return <Empty title={loadError} />
  if (!data) return <Loading />
  const cfg = data.config
  const backUp = () =>
    api<{ operation_id: string }>('POST', `/projects/${project.id}/servers/${s.id}/backups`).then(
      (r) => nav(opPath(project.id, r.operation_id)),
      (e) => setError(e.message),
    )
  return (
    <div className="stack">
      <Panel
        title="Backups"
        actions={
          <>
            {canAdmin && (
              <button className="btn sm" onClick={() => setConfiguring(true)}>
                {cfg ? 'Change' : 'Set up backups'}
              </button>
            )}
            {canRun && cfg && (
              <button className="btn sm primary" onClick={backUp}>
                Back up now
              </button>
            )}
          </>
        }
      >
        <div className="panel-body">
          {!cfg ? (
            <span className="muted">
              Not set up. Orbit backs up every service's volumes and every managed database on this server, encrypted,
              to S3-compatible storage you own or to another of your servers.
            </span>
          ) : (
            <span>
              {cfg.schedule === 'manual' ? 'Manual only' : cfg.schedule === 'daily' ? 'Daily' : 'Weekly'} to{' '}
              <b style={{ fontWeight: 500 }}>
                {cfg.destination === 's3'
                  ? `${providerName(cfg.s3_provider)} · ${cfg.s3?.bucket ?? ''}${cfg.s3?.prefix ? '/' + cfg.s3.prefix : ''}`
                  : `${cfg.target_name ?? 'a removed server'} (${cfg.target_addr})`}
              </b>
              <span className="muted"> · last scheduled run {cfg.last_run_at ? ago(cfg.last_run_at) : 'never'}</span>
            </span>
          )}
          <ErrorNote error={error} />
        </div>
      </Panel>
      <Panel title="Archives">
        {!data.backups.length ? (
          <Empty title="No backups yet" />
        ) : (
          <table className="t">
            <thead>
              <tr>
                <th>Service</th>
                <th>Status</th>
                <th>Location</th>
                <th className="right">Size</th>
                <th className="right">Created</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {data.backups.map((b) => (
                <tr key={b.id}>
                  <td>
                    <div className="name mono">{b.service}</div>
                    <div className="sub">{b.kind}</div>
                  </td>
                  <td>
                    <StatusText tone={b.status === 'succeeded' ? 'ok' : 'err'}>{b.status}</StatusText>
                    {b.error && <div className="sub">{b.error}</div>}
                  </td>
                  <td className="mono muted" style={{ fontSize: 11 }}>
                    {b.location || '—'}
                  </td>
                  <td className="right mono">{b.size ? bytes(b.size) : '—'}</td>
                  <td className="right muted">{datetime(b.created_at)}</td>
                  <td className="right">
                    {canAdmin && b.status === 'succeeded' && (
                      <button className="btn ghost sm" onClick={() => setRestoring(b)}>
                        Restore
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
      {data.runs.length > 0 && (
        <Panel title="Recent runs">
          <div className="list">
            {data.runs.map((o) => (
              <Link key={o.id} className="list-item" to={opPath(project.id, o.id)}>
                <StatusText tone={o.status === 'succeeded' ? 'ok' : o.status === 'failed' ? 'err' : 'info'}>
                  {o.status}
                </StatusText>
                <span className="mono">{o.kind.replace('server.', '')}</span>
                <span className="muted">{o.target}</span>
                <span className="spacer" />
                {o.error && <span className="muted">{o.error}</span>}
                <span className="faint">{ago(o.created_at)}</span>
              </Link>
            ))}
          </div>
        </Panel>
      )}
      {configuring && <BackupConfigDialog s={s} cfg={cfg} onClose={() => setConfiguring(false)} onSaved={reload} />}
      {restoring && (
        <Confirm
          title={`Restore ${restoring.service}?`}
          action="Restore"
          danger
          typeToConfirm={restoring.service.split('/')[1]}
          onClose={() => setRestoring(undefined)}
          onConfirm={() =>
            api<{ operation_id: string }>(
              'POST',
              `/projects/${project.id}/servers/${s.id}/backups/${restoring.id}/restore`,
              { confirm: restoring.service.split('/')[1] },
            ).then((r) => nav(opPath(project.id, r.operation_id)))
          }
        >
          The service is stopped and its data is replaced with the backup from {datetime(restoring.created_at)}. A
          snapshot of the current data is taken first, so you can undo this.
        </Confirm>
      )}
    </div>
  )
}

function BackupConfigDialog({
  s,
  cfg,
  onClose,
  onSaved,
}: {
  s: Server
  cfg: BackupConfig | null
  onClose: () => void
  onSaved: () => void
}) {
  const { project } = useProject()
  const [servers] = useApi<Server[]>(`/projects/${project.id}/servers`)
  const targets = servers?.filter((t) => t.id !== s.id) ?? []
  const [schedule, setSchedule] = useState(cfg?.schedule ?? 'daily')
  const [dest, setDest] = useState(cfg?.destination ?? 's3')
  const [s3, setS3] = useState({
    provider: cfg?.s3_provider || 'aws',
    region: cfg?.s3?.region ?? '',
    endpoint: cfg?.s3?.endpoint ?? '',
    bucket: cfg?.s3?.bucket ?? '',
    prefix: cfg?.s3?.prefix ?? `orbit/${s.name}`,
    access_key: '',
    secret_key: '',
  })
  const [target, setTarget] = useState(cfg?.target_server_id ?? '')
  const [addr, setAddr] = useState(cfg?.target_addr ?? '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const targetId = target || targets[0]?.id || ''
  const keepKeys = cfg?.destination === 's3'
  const needsEndpoint = s3.provider === 'r2' || s3.provider === 'other'
  const setS = (k: keyof typeof s3) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    setS3({ ...s3, [k]: e.target.value })
  const save = () => {
    setBusy(true)
    api<{ free_bytes: number }>('PUT', `/projects/${project.id}/servers/${s.id}/backup-config`, {
      schedule,
      destination: dest,
      ...(dest === 's3' ? { s3 } : { target_server_id: targetId, target_addr: addr }),
    }).then(
      () => {
        onSaved()
        onClose()
      },
      (e) => {
        setError(e.message)
        setBusy(false)
      },
    )
  }
  return (
    <Dialog
      title={`Backups for ${s.name}`}
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" onClick={save} disabled={busy || (dest === 'server' && !targetId)}>
            {busy ? 'Checking…' : 'Save'}
          </button>
        </>
      }
    >
      <div className="grid-2">
        <label className="field">
          <span>Schedule</span>
          <select className="select" value={schedule} onChange={(e) => setSchedule(e.target.value as typeof schedule)}>
            <option value="daily">Daily</option>
            <option value="weekly">Weekly</option>
            <option value="manual">Manual only</option>
          </select>
        </label>
        <label className="field">
          <span>Store backups in</span>
          <select className="select" value={dest} onChange={(e) => setDest(e.target.value as typeof dest)}>
            <option value="s3">S3-compatible storage</option>
            <option value="server">Another server</option>
          </select>
        </label>
      </div>
      {dest === 's3' ? (
        <div className="grid-2">
          <label className="field">
            <span>Provider</span>
            <select className="select" value={s3.provider} onChange={setS('provider')}>
              {PROVIDERS.map(([v, l]) => (
                <option key={v} value={v}>
                  {l}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>Region</span>
            <input className="input mono" placeholder="us-east-1" value={s3.region} onChange={setS('region')} />
          </label>
          {needsEndpoint && (
            <label className="field" style={{ gridColumn: '1 / -1' }}>
              <span>Endpoint</span>
              <input
                className="input mono"
                placeholder="https://<account>.r2.cloudflarestorage.com"
                value={s3.endpoint}
                onChange={setS('endpoint')}
              />
            </label>
          )}
          <label className="field">
            <span>Bucket</span>
            <input className="input mono" value={s3.bucket} onChange={setS('bucket')} />
          </label>
          <label className="field">
            <span>Folder</span>
            <input className="input mono" value={s3.prefix} onChange={setS('prefix')} />
          </label>
          <label className="field">
            <span>Access key</span>
            <input
              className="input mono"
              placeholder={keepKeys ? 'unchanged' : ''}
              value={s3.access_key}
              onChange={setS('access_key')}
              autoComplete="off"
            />
          </label>
          <label className="field">
            <span>Secret key</span>
            <input
              className="input mono"
              type="password"
              placeholder={keepKeys ? 'unchanged' : ''}
              value={s3.secret_key}
              onChange={setS('secret_key')}
              autoComplete="new-password"
            />
          </label>
        </div>
      ) : !targets.length ? (
        <div className="callout warn">Connect another server to this project to use it as a backup target.</div>
      ) : (
        <div className="grid-2">
          <label className="field">
            <span>Target server</span>
            <select className="select" value={targetId} onChange={(e) => setTarget(e.target.value)}>
              {targets.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                  {t.status === 'offline' ? ' (offline)' : ''}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>SSH address (optional)</span>
            <input
              className="input mono"
              placeholder="its IP, port 22"
              value={addr}
              onChange={(e) => setAddr(e.target.value)}
            />
            <span className="hint">The target gets a restricted user that can only receive this server's backups.</span>
          </label>
        </div>
      )}
      <div className="muted">
        Archives are encrypted on this server before upload. Orbit keeps the 7 most recent per service. Saving checks
        that the destination is writable.
      </div>
      <ErrorNote error={error} />
    </Dialog>
  )
}
