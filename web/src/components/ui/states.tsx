import { ReactNode } from 'react'

export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="empty">
      <b>{title}</b>
      {children}
    </div>
  )
}

export function Loading() {
  return <div className="empty">Loading…</div>
}

export function ErrorNote({ error }: { error?: string }) {
  return error ? <div className="callout err">{error}</div> : null
}
