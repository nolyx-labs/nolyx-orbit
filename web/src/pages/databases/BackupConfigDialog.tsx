import { useState } from 'react'
import { useProject } from '../../components/layout/context'
import { Dialog } from '../../components/ui/Dialog'
import { ErrorNote } from '../../components/ui/states'
import { api } from '../../lib/api'
import { Database } from '../../lib/types'

export function BackupConfigDialog({ d, onClose, onSaved }: { d: Database; onClose: () => void; onSaved: () => void }) {
  const { project } = useProject()
  const cfg = d.backup_config
  const [f, setF] = useState({
    enabled: cfg?.enabled ?? true,
    interval_hours: String(cfg?.interval_hours ?? 24),
    retention: String(cfg?.retention ?? 7),
    s3: cfg?.s3 ?? false,
  })
  const [s3, setS3] = useState({
    bucket: '',
    prefix: `orbit/${d.name}`,
    region: 'us-east-1',
    endpoint: '',
    access_key: '',
    secret_key: '',
  })
  const [error, setError] = useState<string>()
  const keepS3 = cfg?.s3 && f.s3 && !s3.bucket
  const save = () =>
    api('PUT', `/projects/${project.id}/databases/${d.id}/backup-config`, {
      enabled: f.enabled,
      interval_hours: +f.interval_hours,
      retention: +f.retention,
      s3: f.s3 && !keepS3 ? s3 : null,
      keep_s3: !!keepS3,
    }).then(
      () => {
        onSaved()
        onClose()
      },
      (e) => setError(e.message),
    )
  const setS = (k: keyof typeof s3) => (e: React.ChangeEvent<HTMLInputElement>) => setS3({ ...s3, [k]: e.target.value })
  return (
    <Dialog
      title="Backup schedule"
      onClose={onClose}
      wide
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" onClick={save}>
            Save
          </button>
        </>
      }
    >
      <label className="row">
        <input type="checkbox" checked={f.enabled} onChange={(e) => setF({ ...f, enabled: e.target.checked })} />{' '}
        Automatic backups
      </label>
      <div className="grid-2">
        <label className="field">
          <span>Every (hours)</span>
          <input
            className="input mono"
            value={f.interval_hours}
            onChange={(e) => setF({ ...f, interval_hours: e.target.value })}
          />
        </label>
        <label className="field">
          <span>Keep (backups)</span>
          <input
            className="input mono"
            value={f.retention}
            onChange={(e) => setF({ ...f, retention: e.target.value })}
          />
        </label>
      </div>
      <label className="row">
        <input type="checkbox" checked={f.s3} onChange={(e) => setF({ ...f, s3: e.target.checked })} /> Upload to
        S3-compatible storage
      </label>
      {f.s3 && (
        <>
          {cfg?.s3 && (
            <div className="muted" style={{ fontSize: 12 }}>
              S3 credentials are stored. Leave the fields empty to keep them.
            </div>
          )}
          <div className="grid-2">
            <label className="field">
              <span>Bucket</span>
              <input className="input mono" value={s3.bucket} onChange={setS('bucket')} />
            </label>
            <label className="field">
              <span>Prefix</span>
              <input className="input mono" value={s3.prefix} onChange={setS('prefix')} />
            </label>
            <label className="field">
              <span>Region</span>
              <input className="input mono" value={s3.region} onChange={setS('region')} />
            </label>
            <label className="field">
              <span>Endpoint (non-AWS)</span>
              <input className="input mono" placeholder="https://…" value={s3.endpoint} onChange={setS('endpoint')} />
            </label>
            <label className="field">
              <span>Access key</span>
              <input className="input mono" value={s3.access_key} onChange={setS('access_key')} autoComplete="off" />
            </label>
            <label className="field">
              <span>Secret key</span>
              <input
                className="input mono"
                type="password"
                value={s3.secret_key}
                onChange={setS('secret_key')}
                autoComplete="new-password"
              />
            </label>
          </div>
        </>
      )}
      <ErrorNote error={error} />
    </Dialog>
  )
}
