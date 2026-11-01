/** Start a long-running operation and return its id (APIs answer 202 {operation_id}). */
export const opPath = (pid: string, id: string) => `/p/${pid}/operations/${id}`

export function incidentLink(pid: string, i: { target_type: string; target_id: string; server_id: string | null }) {
  switch (i.target_type) {
    case 'database':
      return `/p/${pid}/databases/${i.target_id}`
    case 'app':
      return `/p/${pid}/services/${i.target_id}`
    case 'container':
      return `/p/${pid}/docker`
    default:
      return `/p/${pid}/servers/${i.server_id ?? i.target_id}/metrics`
  }
}

/** The ?next= redirect target, if it's a local path. `//host` and `/\host` would leave the site. */
export const safeNext = (next: string | null) => (next && /^\/(?![/\\])/.test(next) ? next : '/')

/** The invitation token when sign-up was reached from an invite link (/invite/:token). */
export const inviteToken = (next: string) => next.match(/^\/invite\/([^/?#]+)/)?.[1]
