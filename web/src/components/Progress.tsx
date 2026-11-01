import { useEffect, useRef } from 'react'

/** Step list for deployments and operations: done ✓, failed ✕, running pulse, pending ·. */
export function StepList({ plan, steps, status }: { plan: string[]; steps: Record<string, string>; status: string }) {
  const names = plan.length ? plan : Object.keys(steps)
  if (!names.length)
    return <div className="muted">{status === 'running' ? 'Waiting for the agent…' : `Finished: ${status}`}</div>
  return (
    <div className="steps">
      {names.map((name) => {
        const st = steps[name] ?? 'pending'
        return (
          <div key={name} className={`step ${st}`}>
            <span className="ic">{st === 'done' ? '✓' : st === 'failed' ? '✕' : st === 'running' ? '' : '·'}</span>
            <span>{name}</span>
          </div>
        )
      })}
    </div>
  )
}

export function LogBox({
  log,
  empty = 'Waiting for agent…',
  max = 360,
}: {
  log: string
  empty?: string
  max?: number
}) {
  const ref = useRef<HTMLPreElement>(null)
  useEffect(() => {
    ref.current?.scrollTo(0, ref.current.scrollHeight)
  }, [log])
  return (
    <pre ref={ref} className="code logs" style={{ maxHeight: max }}>
      {log || empty}
    </pre>
  )
}
