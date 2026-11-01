import { ReactNode } from 'react'

export function Panel({
  title,
  actions,
  children,
  pad,
}: {
  title?: ReactNode
  actions?: ReactNode
  children: ReactNode
  pad?: boolean
}) {
  return (
    <section className="panel">
      {title && (
        <div className="panel-head">
          <h3>{title}</h3>
          <div className="spacer" />
          {actions}
        </div>
      )}
      {pad ? <div className="panel-body">{children}</div> : children}
    </section>
  )
}
