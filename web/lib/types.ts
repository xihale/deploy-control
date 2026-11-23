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
  url?: string
  hosts?: string[]
  ref_regex?: string
  events?: string[]
  script: string
  has_webhook_secret: boolean
  run_as?: string
}

export interface DeployJob {
  id: number
  site: string
  trigger_type: 'push' | 'release' | 'manual' | string
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

export type AnalyticsRangeName = 'today' | '7d' | '30d' | '90d'
export type AnalyticsDimName =
  | 'path'
  | 'referrer'
  | 'browser'
  | 'os'
  | 'device'
  | 'status'
  | 'error_path'

export interface AnalyticsDateRange {
  from: string
  to: string
  days: number
}

export interface AnalyticsSummary {
  today_dau: number
  yesterday_dau: number
  wau: number
  mau: number
  range_uv: number
  today_pv: number
  range_pv: number
  range_requests: number
  range_bytes: number
  err_4xx: number
  err_5xx: number
  bot_hits: number
  avg_duration_ms: number
  pv_per_uv: number
  engaged_rate: number
}

export interface AnalyticsDailyPoint {
  day: string
  dau: number
  pv: number
  requests: number
  bytes: number
  err_4xx: number
  err_5xx: number
  bot_hits: number
  avg_duration_ms: number
  pv_per_uv: number
}

export interface AnalyticsHourlyPoint {
  hour: string
  pv: number
  requests: number
  bytes: number
  err_4xx: number
  err_5xx: number
}

export interface AnalyticsSiteRow {
  site: string
  url?: string
  hosts: string[]
  today_dau: number
  yesterday_dau: number
  wau: number
  mau: number
  range_uv: number
  today_pv: number
  range_pv: number
  range_requests: number
  range_bytes: number
  err_4xx: number
  err_5xx: number
  bot_hits: number
  avg_duration_ms: number
  pv_per_uv: number
}

export interface AnalyticsOverview {
  enabled: boolean
  timezone: string
  range: AnalyticsDateRange
  summary: AnalyticsSummary
  series: AnalyticsDailyPoint[]
  sites: AnalyticsSiteRow[]
}

export interface AnalyticsSiteReport {
  enabled: boolean
  timezone: string
  site: string
  url?: string
  hosts: string[]
  range: AnalyticsDateRange
  summary: AnalyticsSummary
  daily: AnalyticsDailyPoint[]
  hourly: AnalyticsHourlyPoint[]
}

export interface AnalyticsDimensionRow {
  key: string
  pv: number
  requests: number
  bytes: number
  err_count: number
  avg_ms: number
  share: number
}

export interface AnalyticsDimension {
  enabled: boolean
  site: string
  dim: AnalyticsDimName
  range: AnalyticsDateRange
  total: number
  total_pv: number
  total_requests: number
  total_bytes: number
  rows: AnalyticsDimensionRow[]
}
