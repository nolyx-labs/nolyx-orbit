import qrcode from 'qrcode-generator'
import { useEffect, useState } from 'react'
import { useProject } from '../../components/layout/context'
import { Dialog } from '../../components/ui/Dialog'
import { PageHead } from '../../components/ui/PageHead'
import { Panel } from '../../components/ui/Panel'
import { Copy } from '../../components/ui/controls'
import { ErrorNote } from '../../components/ui/states'
import { Badge } from '../../components/ui/status'
import { api, useApi } from '../../lib/api'
import { ago } from '../../lib/format'

export function Account() {
  const { me, reloadMe } = useProject()
  const [pw, setPw] = useState({ current_password: '', new_password: '' })
  const [pwMsg, setPwMsg] = useState<string>()
  const [pwError, setPwError] = useState<string>()
  const [dialog, setDialog] = useState<'setup' | 'disable' | 'codes' | null>(null)
  const [sso] = useApi<{ enabled: boolean; name: string; subject: string | null; linked_at: string | null }>(
    '/account/sso',
  )
  const close = () => {
    setDialog(null)
    reloadMe()
  }
  return (
    <div className="page">
      <PageHead title="Account" sub={me.user.email} />
      <div className="stack">
        <Panel title="Password">
          <form
            className="panel-body form"
            style={{ maxWidth: 360 }}
            onSubmit={(e) => {
              e.preventDefault()
              setPwError(undefined)
              api('POST', '/account/password', pw).then(
                () => {
                  setPw({ current_password: '', new_password: '' })
                  setPwMsg('Password changed. Other sessions were signed out.')
                },
                (e) => setPwError(e.message),
              )
            }}
          >
            <label className="field">
              <span>Current password</span>
              <input
                className="input"
                type="password"
                required
                autoComplete="current-password"
                value={pw.current_password}
                onChange={(e) => setPw({ ...pw, current_password: e.target.value })}
              />
            </label>
            <label className="field">
              <span>New password</span>
              <input
                className="input"
                type="password"
                required
                minLength={10}
                autoComplete="new-password"
                value={pw.new_password}
                onChange={(e) => {
                  setPw({ ...pw, new_password: e.target.value })
                  setPwMsg(undefined)
                }}
              />
              <span className="hint">At least 10 characters.</span>
            </label>
            <ErrorNote error={pwError} />
            <div className="row">
              <button className="btn">Change password</button>
              {pwMsg && <span className="faint">{pwMsg}</span>}
            </div>
          </form>
        </Panel>
        <Panel
          title={
            <>Two-factor authentication {me.user.mfa_enabled ? <Badge tone="ok">On</Badge> : <Badge>Off</Badge>}</>
          }
          actions={
            me.user.mfa_enabled ? (
              <>
                <button className="btn sm" onClick={() => setDialog('codes')}>
                  New recovery codes
                </button>
                <button className="btn sm danger" onClick={() => setDialog('disable')}>
                  Turn off
                </button>
              </>
            ) : (
              <button className="btn sm primary" onClick={() => setDialog('setup')}>
                Set up
              </button>
            )
          }
        >
          <div className="panel-body muted">
            Signing in asks for a code from an authenticator app (1Password, Google Authenticator, Authy…) as well as
            your password. Orbit can run commands on every connected server, so this is strongly recommended.
          </div>
        </Panel>
        {sso?.enabled && (
          <Panel
            title={<>Single sign-on {sso.subject ? <Badge tone="ok">Linked</Badge> : <Badge>Not linked</Badge>}</>}
          >
            <div className="panel-body muted">
              {sso.subject ? (
                <>
                  Linked to your {sso.name} identity <span className="mono">{sso.subject}</span> {ago(sso.linked_at)}.
                  Only that identity can sign in to this account with {sso.name}.
                </>
              ) : (
                <>Use Sign in with {sso.name} on the sign-in page to link your account.</>
              )}
            </div>
          </Panel>
        )}
      </div>
      {dialog === 'setup' && <SetupDialog onClose={close} />}
      {dialog === 'disable' && <DisableDialog onClose={close} />}
      {dialog === 'codes' && <NewCodesDialog onClose={close} />}
    </div>
  )
}

function RecoveryCodes({ codes, onClose }: { codes: string[]; onClose: () => void }) {
  return (
    <Dialog
      title="Save your recovery codes"
      onClose={onClose}
      footer={
        <button className="btn primary" onClick={onClose}>
          I've saved them
        </button>
      }
    >
      <div className="callout warn">
        Each code works once, in place of an authenticator code. Store them somewhere safe. They're shown only once.
      </div>
      <div className="copy-row">
        <pre className="code">{codes.join('\n')}</pre>
        <Copy text={codes.join('\n')} />
      </div>
    </Dialog>
  )
}

function CodeField({ value, onChange, hint }: { value: string; onChange: (v: string) => void; hint?: string }) {
  return (
    <label className="field">
      <span>Code</span>
      <input
        className="input mono"
        autoComplete="one-time-code"
        inputMode="numeric"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        autoFocus
      />
      {hint && <span className="hint">{hint}</span>}
    </label>
  )
}

function SetupDialog({ onClose }: { onClose: () => void }) {
  const [setup, setSetup] = useState<{ secret: string; uri: string; qr: string }>()
  const [code, setCode] = useState('')
  const [codes, setCodes] = useState<string[]>()
  const [error, setError] = useState<string>()
  useEffect(() => {
    let ignore = false // StrictMode runs this twice in dev; only the last secret is stored, so show only that one
    api<{ secret: string; uri: string }>('POST', '/account/mfa/setup').then(
      (r) => {
        if (ignore) return
        const q = qrcode(0, 'M')
        q.addData(r.uri)
        q.make()
        setSetup({ ...r, qr: q.createDataURL(4, 2) })
      },
      (e) => !ignore && setError(e.message),
    )
    return () => {
      ignore = true
    }
  }, [])
  if (codes) return <RecoveryCodes codes={codes} onClose={onClose} />
  return (
    <Dialog
      title="Set up two-factor authentication"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            disabled={!setup || code.length < 6}
            onClick={() =>
              api<{ recovery_codes: string[] }>('POST', '/account/mfa/enable', { code }).then(
                (r) => setCodes(r.recovery_codes),
                (e) => setError(e.message),
              )
            }
          >
            Turn on
          </button>
        </>
      }
    >
      {setup && (
        <>
          <div className="muted">Scan this with your authenticator app, then enter the 6-digit code it shows.</div>
          <img
            src={setup.qr}
            alt="QR code for your authenticator app"
            style={{ alignSelf: 'center', background: '#fff' }}
          />
          <div className="copy-row">
            <pre className="code">{setup.secret}</pre>
            <Copy text={setup.secret} />
          </div>
          <CodeField value={code} onChange={setCode} />
        </>
      )}
      <ErrorNote error={error} />
    </Dialog>
  )
}

function DisableDialog({ onClose }: { onClose: () => void }) {
  const [f, setF] = useState({ password: '', code: '' })
  const [error, setError] = useState<string>()
  return (
    <Dialog
      title="Turn off two-factor authentication"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn danger solid"
            disabled={!f.password || !f.code}
            onClick={() => api('POST', '/account/mfa/disable', f).then(onClose, (e) => setError(e.message))}
          >
            Turn off
          </button>
        </>
      }
    >
      <label className="field">
        <span>Password</span>
        <input
          className="input"
          type="password"
          autoComplete="current-password"
          value={f.password}
          onChange={(e) => setF({ ...f, password: e.target.value })}
        />
      </label>
      <CodeField value={f.code} onChange={(code) => setF({ ...f, code })} hint="An authenticator or recovery code." />
      <ErrorNote error={error} />
    </Dialog>
  )
}

function NewCodesDialog({ onClose }: { onClose: () => void }) {
  const [code, setCode] = useState('')
  const [codes, setCodes] = useState<string[]>()
  const [error, setError] = useState<string>()
  if (codes) return <RecoveryCodes codes={codes} onClose={onClose} />
  return (
    <Dialog
      title="New recovery codes"
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn primary"
            disabled={!code}
            onClick={() =>
              api<{ recovery_codes: string[] }>('POST', '/account/mfa/recovery-codes', { code }).then(
                (r) => setCodes(r.recovery_codes),
                (e) => setError(e.message),
              )
            }
          >
            Generate
          </button>
        </>
      }
    >
      <div className="muted">Your old recovery codes stop working.</div>
      <CodeField value={code} onChange={setCode} hint="A code from your authenticator app." />
      <ErrorNote error={error} />
    </Dialog>
  )
}
