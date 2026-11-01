import { useProject } from '../../components/layout/context'
import { useApi } from '../../lib/api'
import { Server } from '../../lib/types'

export function ServerSelect({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const { project } = useProject()
  const [servers] = useApi<Server[]>(`/projects/${project.id}/servers`)
  const v = value || servers?.[0]?.id || ''
  if (servers && !value && v) setTimeout(() => onChange(v))
  return (
    <select className="select" value={v} onChange={(e) => onChange(e.target.value)}>
      {servers?.map((s) => (
        <option key={s.id} value={s.id}>
          {s.name}
          {s.status === 'offline' ? ' (offline)' : ''}
        </option>
      ))}
    </select>
  )
}
