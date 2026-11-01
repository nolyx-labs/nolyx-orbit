import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { BrandMark } from '../../components/ui/BrandMark'
import { ErrorNote, Loading } from '../../components/ui/states'
import { api, useApi } from '../../lib/api'

export function InviteAccept() {
  const { token } = useParams()
  const nav = useNavigate()
  const [inv, err] = useApi<{ org: string; email: string; role: string }>(`/invites/${token}`)
  const [error, setError] = useState<string>()
  const accept = () =>
    api<{ project_id: string }>('POST', '/invites/accept', { token }).then(
      (r) => nav(`/p/${r.project_id}/overview`),
      (e) => {
        if (e.status === 401) nav(`/signup?next=/invite/${token}`)
        else setError(e.message)
      },
    )
  return (
    <div className="auth">
      <div className="auth-box form">
        <BrandMark />
        {err ? (
          <>
            <h1>Invitation unavailable</h1>
            <div className="muted">{err}</div>
          </>
        ) : !inv ? (
          <Loading />
        ) : (
          <>
            <h1>Join {inv.org}</h1>
            <div className="muted">
              You were invited as <b>{inv.role}</b> ({inv.email}). Sign in or create an account with that email to
              accept.
            </div>
            <ErrorNote error={error} />
            <button className="btn primary" style={{ height: 32, justifyContent: 'center' }} onClick={accept}>
              Accept invitation
            </button>
            <div className="muted" style={{ textAlign: 'center' }}>
              <Link to={`/login?next=/invite/${token}`} style={{ color: 'var(--text)' }}>
                Sign in
              </Link>{' '}
              ·{' '}
              <Link to={`/signup?next=/invite/${token}`} style={{ color: 'var(--text)' }}>
                Create account
              </Link>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
