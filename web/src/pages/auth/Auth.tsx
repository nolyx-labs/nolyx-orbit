import { FormEvent, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { BrandMark } from '../../components/ui/BrandMark'
import { ErrorNote } from '../../components/ui/states'
import { api } from '../../lib/api'

export function Auth({ mode }: { mode: 'login' | 'signup' }) {
  const nav = useNavigate()
  const [params] = useSearchParams()
  const next = params.get('next')?.startsWith('/') ? params.get('next')! : '/'
  const [form, setForm] = useState({ name: '', email: '', password: '' })
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm({ ...form, [k]: e.target.value })

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError(undefined)
    try {
      await api('POST', `/auth/${mode}`, mode === 'login' ? { email: form.email, password: form.password } : form)
      nav(next)
    } catch (err: any) {
      setError(err.message)
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
        <ErrorNote error={error} />
        <button className="btn primary" style={{ height: 32, justifyContent: 'center' }} disabled={busy}>
          {busy ? 'Please wait…' : mode === 'login' ? 'Sign in' : 'Create account'}
        </button>
        <div className="muted" style={{ textAlign: 'center' }}>
          {mode === 'login' ? (
            <>
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
