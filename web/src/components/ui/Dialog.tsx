import { ReactNode, useEffect } from 'react'

export function Dialog({
  title,
  children,
  footer,
  onClose,
  wide,
}: {
  title: string
  children: ReactNode
  footer?: ReactNode
  onClose: () => void
  wide?: boolean
}) {
  useEffect(() => {
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', esc)
    return () => window.removeEventListener('keydown', esc)
  }, [onClose])
  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className={`dialog ${wide ? 'wide' : ''}`} role="dialog" aria-modal aria-label={title}>
        <div className="body">
          <h2>{title}</h2>
          {children}
        </div>
        {footer && <div className="foot">{footer}</div>}
      </div>
    </div>
  )
}
