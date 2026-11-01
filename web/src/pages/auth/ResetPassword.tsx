import { FormEvent, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { BrandMark } from '../../components/ui/BrandMark'
import { ErrorNote } from '../../components/ui/states'
import { api } from '../../lib/api'

/** /forgot-password asks for an email; /reset-password/:token sets the new password. */
export function ResetPassword() {
  const { token } = useParams()
  const nav = useNavigate()
  const [form, setForm] = useState({ email: '', password: '', code: '' })
  const [mfa, setMfa] = useState(false)
  const [sent, setSent] = useState(false)
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm({ ...form, [k]: e.target.value })

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(undefined)
    try {
      if (token) {
        await api('POST', '/auth/password-reset/confirm', { token, password: form.password, code: form.code })
        nav('/login')
      } else {
        await api('POST', '/auth/password-reset', { email: form.email })
        setSent(true)
      }
    } catch (err: any) {
      if (err.data?.mfa_required) {
        setError(mfa ? err.message : undefined)
        setMfa(true)
      } else setError(err.message)
    }
    setBusy(false)
  }

  return (
    <div className="auth">
      <form className="auth-box form" onSubmit={submit}>
        <div>
          <BrandMark />
          <h1>{token ? 'Choose a new password' : 'Reset your password'}</h1>
        </div>
        {sent ? (
          <div className="muted">
            If an account exists for {form.email}, we've emailed it a reset link. It expires in one hour.
          </div>
        ) : token ? (
          <>
            <label className="field">
              <span>New password</span>
              <input
                className="input"
                type="password"
                required
                minLength={10}
                value={form.password}
                onChange={set('password')}
                autoComplete="new-password"
                autoFocus
              />
              <span className="hint">At least 10 characters. You'll be signed out everywhere.</span>
            </label>
            {mfa && (
              <label className="field">
                <span>Two-factor code</span>
                <input
                  className="input mono"
                  required
                  value={form.code}
                  onChange={set('code')}
                  autoComplete="one-time-code"
                  autoFocus
                />
                <span className="hint">The 6-digit code from your authenticator app, or a recovery code.</span>
              </label>
            )}
          </>
        ) : (
          <label className="field">
            <span>Email</span>
            <input
              className="input"
              type="email"
              required
              value={form.email}
              onChange={set('email')}
              autoComplete="email"
              autoFocus
            />
          </label>
        )}
        <ErrorNote error={error} />
        {!sent && (
          <button className="btn primary" style={{ height: 32, justifyContent: 'center' }} disabled={busy}>
            {busy ? 'Please wait…' : token ? 'Set password' : 'Send reset link'}
          </button>
        )}
        <div className="muted" style={{ textAlign: 'center' }}>
          <Link to="/login" style={{ color: 'var(--text)' }}>
            Back to sign in
          </Link>
        </div>
      </form>
    </div>
  )
}
