import { ReactNode, useEffect, useState } from 'react'
import { ErrorNote } from './states'

/** Confirmation dialog. With `typeToConfirm`, the action stays disabled until the exact text is typed. */
export function Confirm({
  title,
  children,
  action,
  danger,
  typeToConfirm,
  onConfirm,
  onClose,
}: {
  title: string
  children: ReactNode
  action: string
  danger?: boolean
  typeToConfirm?: string
  onConfirm: () => Promise<unknown>
  onClose: () => void
}) {
  const [typed, setTyped] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  useEffect(() => {
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && !busy && onClose()
    window.addEventListener('keydown', esc)
    return () => window.removeEventListener('keydown', esc)
  }, [busy, onClose])
  const ok = !typeToConfirm || typed === typeToConfirm
  return (
    <div className="overlay" onMouseDown={(e) => e.target === e.currentTarget && !busy && onClose()}>
      <div className="dialog" role="dialog" aria-modal aria-labelledby="dlg-title">
        <div className="body">
          <h2 id="dlg-title">{title}</h2>
          <div className="muted">{children}</div>
          {typeToConfirm && (
            <label className="field">
              <span>
                Type{' '}
                <span className="mono" style={{ color: 'var(--text)' }}>
                  {typeToConfirm}
                </span>{' '}
                to confirm
              </span>
              <input
                className="input mono"
                autoFocus
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                autoComplete="off"
                spellCheck={false}
              />
            </label>
          )}
          <ErrorNote error={error} />
        </div>
        <div className="foot">
          <button className="btn" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button
            className={`btn ${danger ? 'danger solid' : 'primary'}`}
            disabled={!ok || busy}
            autoFocus={!typeToConfirm}
            onClick={async () => {
              setBusy(true)
              setError(undefined)
              try {
                await onConfirm()
                onClose()
              } catch (e: any) {
                setError(e.message)
                setBusy(false)
              }
            }}
          >
            {busy ? 'Working…' : action}
          </button>
        </div>
      </div>
    </div>
  )
}
