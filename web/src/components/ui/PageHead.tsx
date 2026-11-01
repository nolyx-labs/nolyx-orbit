import { ReactNode } from 'react'

export function PageHead({
  title,
  sub,
  crumbs,
  children,
}: {
  title: ReactNode
  sub?: ReactNode
  crumbs?: ReactNode
  children?: ReactNode
}) {
  return (
    <div className="page-head">
      <div>
        {crumbs && <div className="crumbs">{crumbs}</div>}
        <h1>{title}</h1>
        {sub && <div className="sub">{sub}</div>}
      </div>
      <div className="spacer" />
      {children}
    </div>
  )
}
