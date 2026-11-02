export const ENVIRONMENTS = ['production', 'staging', 'development'] as const

export type ServerGroup = {
  id: string
  name: string
  servers: number
}
