import { useState } from 'react'
import { useCan, useProject } from '../../components/layout/context'
import { Confirm } from '../../components/ui/Confirm'
import { Meter } from '../../components/ui/Meter'
import { Panel } from '../../components/ui/Panel'
import { Empty, ErrorNote, Loading } from '../../components/ui/states'
import { api } from '../../lib/api'
import { bytes, ratio } from '../../lib/format'
import { useInsight } from './insight'

interface DFLine {
  count: number
  size: number
  reclaimable: number
}

interface DiskUsage {
  path: string
  dirs: { path: string; size: number }[]
  partial: boolean
  note?: string
  docker?: { images: DFLine; containers: DFLine; volumes: DFLine; build_cache: DFLine }
}

export function ServerDisk({ sid }: { sid: string }) {
  const { project } = useProject()
  const canRun = useCan('member')
  const [ins] = useInsight(sid)
  const [mount, setMount] = useState('/')
  const [usage, setUsage] = useState<DiskUsage>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const [pruning, setPruning] = useState(false)
  const [freed, setFreed] = useState<number>()
  if (!canRun) return <Empty title="Looking at disk usage needs the member role" />
  if (!ins) return <Loading />
  const disks = ins.inventory?.specs.disks ?? []
  const analyze = () => {
    setBusy(true)
    setError(undefined)
    api<DiskUsage>('GET', `/projects/${project.id}/servers/${sid}/disk?path=${encodeURIComponent(mount)}`)
      .then(setUsage, (e) => setError(e.message))
      .finally(() => setBusy(false))
  }
  const largest = usage?.dirs[0]?.size ?? 0
  const df = usage?.docker
  return (
    <div className="stack">
      <Panel title="What's using the disk">
        <div className="panel-body row" style={{ flexWrap: 'wrap' }}>
          <select
            className="select"
            style={{ width: 280 }}
            value={mount}
            onChange={(e) => setMount(e.target.value)}
            aria-label="Filesystem"
          >
            {disks.map((d) => (
              <option key={d.mount} value={d.mount}>
                {d.mount} · {bytes(d.used)} of {bytes(d.size)} used
              </option>
            ))}
          </select>
          <button className="btn primary" disabled={busy || !disks.length} onClick={analyze}>
            {busy ? 'Measuring… (up to a minute)' : 'Analyze'}
          </button>
        </div>
        <ErrorNote error={error} />
        {usage?.note && (
          <div className="callout warn" style={{ margin: '0 14px 12px' }}>
            {usage.note}
          </div>
        )}
        {usage?.partial && (
          <div className="callout warn" style={{ margin: '0 14px 12px' }}>
            Measuring took too long and was stopped, so some folders are missing and totals may be low.
          </div>
        )}
        {!usage ? (
          <Empty title="Pick a filesystem to see its largest folders (two levels deep)" />
        ) : (
          !!usage.dirs.length && (
            <table className="t">
              <tbody>
                {usage.dirs.map((d) => (
                  <tr key={d.path}>
                    <td className="mono">{d.path}</td>
                    <td style={{ width: 220 }}>
                      <Meter value={ratio(d.size, largest)} />
                    </td>
                    <td className="right mono">{bytes(d.size)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )
        )}
      </Panel>
      {df && (
        <Panel
          title="Docker"
          actions={
            <button className="btn sm" onClick={() => setPruning(true)}>
              Clean up unused images and build cache
            </button>
          }
        >
          {freed != null && (
            <div className="callout" style={{ margin: 14 }}>
              Freed {bytes(freed)}.
            </div>
          )}
          <table className="t">
            <thead>
              <tr>
                <th>Type</th>
                <th className="right">Count</th>
                <th className="right">Size</th>
                <th className="right">Reclaimable</th>
              </tr>
            </thead>
            <tbody>
              {(
                [
                  ['Images', df.images],
                  ['Containers', df.containers],
                  ['Volumes', df.volumes],
                  ['Build cache', df.build_cache],
                ] as const
              ).map(([label, l]) => (
                <tr key={label}>
                  <td>{label}</td>
                  <td className="right mono">{l.count}</td>
                  <td className="right mono">{bytes(l.size)}</td>
                  <td className="right mono">{bytes(l.reclaimable)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
      )}
      {pruning && (
        <Confirm
          title="Clean up Docker?"
          action="Clean up"
          danger
          onClose={() => setPruning(false)}
          onConfirm={() =>
            api<{ reclaimed: number }>('POST', `/projects/${project.id}/servers/${sid}/docker/prune`).then((r) => {
              setFreed(r.reclaimed)
              analyze()
            })
          }
        >
          Removes every image no container uses and the whole build cache. Containers and volumes are not touched.
          Removed images are downloaded or built again on the next deploy that needs them.
        </Confirm>
      )}
    </div>
  )
}
