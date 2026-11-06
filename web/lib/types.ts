export type DeployStatus =
  | 'queued'
  | 'running'
  | 'succeeded'
  | 'failed'
  | 'superseded'

export interface Site {
  name: string
  repo: string
  branch: string
  script: string
  has_webhook_secret: boolean
  run_as?: string
}

export interface DeployJob {
  id: number
  site: string
  trigger_type: 'push' | 'manual' | string
  branch: string
  sha?: string
  checked_sha?: string
  delivery_id?: string
  status: DeployStatus
  attempts: number
  created_at: string
  started_at?: string
  finished_at?: string
  exit_code?: number
  error?: string
  log_path?: string
}

export interface DeployList {
  total: number
  jobs: DeployJob[]
}

export interface Health {
  status: string
  queued: number
  running: number
}

export interface Me {
  name: string
  role: 'admin' | 'viewer' | string
}
