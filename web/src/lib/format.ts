// ---------- formatting ----------

export function bytes(n: number | null | undefined, digits = 1): string {
  if (n == null) return '—'
  const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0
  while (n >= 1024 && i < u.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toFixed(i === 0 ? 0 : digits)} ${u[i]}`
}

export function bitrate(bytesPerSec: number | null | undefined): string {
  if (bytesPerSec == null) return '—'
  const bits = bytesPerSec * 8
  if (bits >= 1e9) return `${(bits / 1e9).toFixed(1)} Gbps`
  if (bits >= 1e6) return `${(bits / 1e6).toFixed(1)} Mbps`
  if (bits >= 1e3) return `${(bits / 1e3).toFixed(0)} Kbps`
  return `${bits.toFixed(0)} bps`
}

export function pct(n: number | null | undefined): string {
  return n == null ? '—' : `${n.toFixed(n < 10 ? 1 : 0)}%`
}

export function ago(ts: string | null | undefined): string {
  if (!ts) return 'never'
  const s = Math.max(0, (Date.now() - new Date(ts).getTime()) / 1000)
  if (s < 5) return 'just now'
  if (s < 60) return `${Math.floor(s)}s ago`
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  return `${Math.floor(s / 86400)}d ago`
}

export function duration(sec: number | null | undefined): string {
  if (sec == null) return '—'
  if (sec < 60) return `${sec}s`
  if (sec < 3600) return `${Math.floor(sec / 60)}m ${sec % 60}s`
  if (sec < 86400) return `${Math.floor(sec / 3600)}h ${Math.floor((sec % 3600) / 60)}m`
  return `${Math.floor(sec / 86400)}d ${Math.floor((sec % 86400) / 3600)}h`
}

export function clock(ts: string): string {
  return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false })
}

export function datetime(ts: string): string {
  return new Date(ts).toLocaleString([], {
    day: '2-digit',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}

export function ratio(used: number | null | undefined, total: number | null | undefined): number | null {
  return used == null || !total ? null : (used / total) * 100
}

export function num(n: number | null | undefined, digits = 1): string {
  if (n == null) return '—'
  if (Math.abs(n) >= 1000) return n.toLocaleString(undefined, { maximumFractionDigits: 0 })
  return n.toFixed(digits)
}
