import { FormEvent, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { BrandMark } from '../../components/ui/BrandMark'
import { ErrorNote } from '../../components/ui/states'
import { api } from '../../lib/api'
import { inviteToken, safeNext } from '../../lib/paths'

export function Auth({ mode }: { mode: 'login' | 'signup' }) {
  const nav = useNavigate()
  const [params] = useSearchParams()
  const next = safeNext(params.get('next'))
  const [form, setForm] = useState({ name: '', email: '', password: '', code: '' })
  const [mfa, setMfa] = useState(false)
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm({ ...form, [k]: e.target.value })

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(undefined)
    try {
      await api(
        'POST',
        `/auth/${mode}`,
        mode === 'login'
          ? { email: form.email, password: form.password, code: form.code }
          : { name: form.name, email: form.email, password: form.password, invite: inviteToken(next) ?? '' },
      )
      nav(next)
    } catch (err: any) {
      if (err.data?.mfa_required) {
        // The first answer only asks for the code; don't show it as an error.
        setError(mfa ? err.message : undefined)
        setMfa(true)
      } else setError(err.message)
      setBusy(false)
    }
  }

  return (
    <div className="auth">
      <form className="auth-box form" onSubmit={submit}>
        <div>
          <BrandMark />
          <h1>{mode === 'login' ? 'Sign in to Nolyx Orbit' : 'Create your Nolyx Orbit account'}</h1>
          <div className="muted">Your infrastructure. Your servers. Your control plane.</div>
        </div>
        {mfa ? (
          <label className="field">
            <span>Two-factor code</span>
            <input
              className="input mono"
              required
              value={form.code}
              onChange={set('code')}
              autoComplete="one-time-code"
              inputMode="numeric"
              autoFocus
            />
            <span className="hint">The 6-digit code from your authenticator app, or a recovery code.</span>
          </label>
        ) : (
          <>
            {mode === 'signup' && (
              <label className="field">
                <span>Name</span>
                <input className="input" value={form.name} onChange={set('name')} autoComplete="name" />
              </label>
            )}
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
            <label className="field">
              <span>Password</span>
              <input
                className="input"
                type="password"
                required
                minLength={mode === 'signup' ? 10 : undefined}
                value={form.password}
                onChange={set('password')}
                autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
              />
              {mode === 'signup' && <span className="hint">At least 10 characters.</span>}
            </label>
          </>
        )}
        <ErrorNote error={error} />
        <button className="btn primary" style={{ height: 32, justifyContent: 'center' }} disabled={busy}>
          {busy ? 'Please wait…' : mode === 'login' ? (mfa ? 'Verify' : 'Sign in') : 'Create account'}
        </button>
        <div className="muted" style={{ textAlign: 'center' }}>
          {mode === 'login' ? (
            <>
              <Link to="/forgot-password" style={{ color: 'var(--text)' }}>
                Forgot password?
              </Link>
              {' · '}
              No account?{' '}
              <Link
                to={`/signup${next !== '/' ? `?next=${encodeURIComponent(next)}` : ''}`}
                style={{ color: 'var(--text)' }}
              >
                Sign up
              </Link>
            </>
          ) : (
            <>
              Have an account?{' '}
              <Link
                to={`/login${next !== '/' ? `?next=${encodeURIComponent(next)}` : ''}`}
                style={{ color: 'var(--text)' }}
              >
                Sign in
              </Link>
            </>
          )}
        </div>
      </form>
    </div>
  )
}
