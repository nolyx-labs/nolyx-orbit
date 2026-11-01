import { ReactNode } from 'react'
import { Container, Server } from '../../lib/types'

export type Tone = 'ok' | 'warn' | 'err' | 'info' | ''

export function Dot({ tone }: { tone: Tone }) {
  return <span className={`dot ${tone}`} aria-hidden />
}

export function Badge({ tone = '', children }: { tone?: Tone; children: ReactNode }) {
  return <span className={`badge ${tone}`}>{children}</span>
}

export function serverTone(s: Pick<Server, 'status'>): Tone {
  return s.status === 'online' ? 'ok' : 'err'
}

export function containerTone(c: Pick<Container, 'state' | 'health'>): Tone {
  if (c.health === 'unhealthy' || c.state === 'restarting' || c.state === 'dead') return 'err'
  if (c.state === 'running') return c.health === 'starting' ? 'warn' : 'ok'
  if (c.state === 'exited' || c.state === 'created') return ''
  return 'warn'
}

export function StatusText({ tone, children }: { tone: Tone; children: ReactNode }) {
  return (
    <span className="row nowrap" style={{ gap: 7 }}>
      <Dot tone={tone} />
      {children}
    </span>
  )
}
