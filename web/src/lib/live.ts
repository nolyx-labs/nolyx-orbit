import { useEffect, useRef } from 'react'

type Listener = (msg: { topic: string; id: string }) => void

const listeners = new Set<Listener>()

let source: EventSource | null = null

let sourceProject = ''

/** Subscribes to the project's SSE stream; the callback (throttled) runs when a matching topic changes. */
export function useLive(projectId: string | undefined, topics: string[], onChange: () => void, throttleMs = 2000) {
  const cb = useRef(onChange)
  cb.current = onChange
  const key = topics.join(',')
  useEffect(() => {
    if (!projectId) return
    if (sourceProject !== projectId) {
      source?.close()
      source = new EventSource(`/api/v1/projects/${projectId}/stream`)
      sourceProject = projectId
      source.onmessage = (e) => {
        const msg = JSON.parse(e.data)
        listeners.forEach((l) => l(msg))
      }
    }
    let timer: number | undefined
    let last = 0
    const fire = () => {
      last = Date.now()
      timer = undefined
      cb.current()
    }
    const l: Listener = (msg) => {
      if (!key.split(',').includes(msg.topic)) return
      if (timer) return
      timer = window.setTimeout(fire, Math.max(0, throttleMs - (Date.now() - last)))
    }
    listeners.add(l)
    return () => {
      listeners.delete(l)
      clearTimeout(timer)
    }
  }, [projectId, key, throttleMs])
}
