import { useCallback, useEffect, useRef, useState } from 'react'

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public data: any = {},
  ) {
    super(message)
  }
}

export async function api<T = any>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch('/api/v1' + path, {
    method,
    credentials: 'same-origin',
    headers: {
      'X-Orbit-Request': '1',
      ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => ({}))
  if (res.status === 401 && !path.startsWith('/auth/')) {
    window.location.href = '/login?next=' + encodeURIComponent(window.location.pathname)
  }
  if (!res.ok) throw new ApiError(res.status, data.error ?? res.statusText, data)
  return data as T
}

/** Fetches path; returns [data, error, reload]. Keeps stale data while reloading so live refreshes don't flash. */
export function useApi<T = any>(path: string | null) {
  const [data, setData] = useState<T | undefined>()
  const [error, setError] = useState<string>()
  const current = useRef(path)
  current.current = path
  const load = useCallback(() => {
    if (!path) return
    api<T>('GET', path).then(
      (d) => current.current === path && (setData(d), setError(undefined)),
      (e) => current.current === path && setError(e.message),
    )
  }, [path])
  useEffect(() => {
    setData(undefined)
    load()
  }, [load])
  return [data, error, load] as const
}
