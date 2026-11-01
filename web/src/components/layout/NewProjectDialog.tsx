import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../../lib/api'
import { Dialog } from '../ui/Dialog'
import { ErrorNote } from '../ui/states'
import { Org, useProject } from './context'

export function NewProject({ org, onClose }: { org: Org; onClose: () => void }) {
  const { reloadMe } = useProject()
  const nav = useNavigate()
  const [name, setName] = useState('')
  const [error, setError] = useState<string>()
  const submit = async () => {
    try {
      const p = await api<{ id: string }>('POST', `/orgs/${org.id}/projects`, { name })
      reloadMe()
      onClose()
      nav(`/p/${p.id}/overview`)
    } catch (e: any) {
      setError(e.message)
    }
  }
  return (
    <Dialog
      title={`New project in ${org.name}`}
      onClose={onClose}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            Cancel
          </button>
          <button className="btn primary" disabled={!name.trim()} onClick={submit}>
            Create project
          </button>
        </>
      }
    >
      <label className="field">
        <span>Name</span>
        <input
          className="input"
          autoFocus
          placeholder="Staging"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && name.trim() && submit()}
        />
      </label>
      <ErrorNote error={error} />
    </Dialog>
  )
}
