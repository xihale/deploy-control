'use client'

import useSWR from 'swr'
import { useAuth } from '@/components/auth-provider'
import { buildQuery, getJson, getText } from '@/lib/api'
import { isActive } from '@/lib/status'
import type {
  AnalyticsDimName,
  AnalyticsDimension,
  AnalyticsOverview,
  AnalyticsSiteReport,
  DeployJob,
  DeployList,
  DeployStatus,
  Health,
  Me,
  ShieldBansResponse,
  Site,
} from '@/lib/types'

export function useMe() {
  const { token } = useAuth()
  return useSWR(token ? ['/api/me', token] : null, ([p, t]) =>
    getJson<Me>(p, t),
  )
}

export function useSites() {
  const { token } = useAuth()
  return useSWR(token ? ['/api/sites', token] : null, ([p, t]) =>
    getJson<Site[]>(p, t),
  )
}

export function useDeploys(
  params: {
    site?: string
    status?: DeployStatus | ''
    limit?: number
    offset?: number
  },
  refreshInterval = 8000,
) {
  const { token } = useAuth()
  const path = `/api/deploys${buildQuery(params)}`
  return useSWR(
    token ? [path, token] : null,
    ([p, t]) => getJson<DeployList>(p, t),
    { refreshInterval, keepPreviousData: true },
  )
}

export function useDeploy(id: number | null) {
  const { token } = useAuth()
  return useSWR(
    token && id ? [`/api/deploys/${id}`, token] : null,
    ([p, t]) => getJson<DeployJob>(p, t),
    {
      refreshInterval: (job) => (!job || isActive(job.status) ? 3000 : 0),
    },
  )
}

export function useDeployLog(id: number | null, bytes: number, active: boolean) {
  const { token } = useAuth()
  return useSWR(
    token && id ? [`/api/deploys/${id}/log?bytes=${bytes}`, token] : null,
    ([p, t]) => getText(p, t),
    { refreshInterval: active ? 3000 : 0, keepPreviousData: true },
  )
}

export function useHealth() {
  const { token } = useAuth()
  return useSWR(
    token ? ['/healthz', token] : null,
    ([p, t]) => getJson<Health>(p, t),
    { refreshInterval: 10000 },
  )
}

export function useAnalyticsOverview(params: {
  range?: string
  from?: string
  to?: string
}) {
  const { token } = useAuth()
  const path = `/api/analytics/overview${buildQuery(params)}`
  return useSWR(
    token ? [path, token] : null,
    ([p, t]) => getJson<AnalyticsOverview>(p, t),
    { refreshInterval: 30000, keepPreviousData: true },
  )
}

export function useAnalyticsSite(
  site: string | null,
  params: { range?: string; from?: string; to?: string },
) {
  const { token } = useAuth()
  const path =
    site && site !== '_all'
      ? `/api/analytics/sites/${encodeURIComponent(site)}${buildQuery(params)}`
      : null
  return useSWR(
    token && path ? [path, token] : null,
    ([p, t]) => getJson<AnalyticsSiteReport>(p, t),
    { refreshInterval: 30000, keepPreviousData: true },
  )
}

export function useAnalyticsDimensions(params: {
  site: string
  dim: AnalyticsDimName
  range?: string
  from?: string
  to?: string
  q?: string
  sort?: string
  order?: 'asc' | 'desc'
  limit?: number
  offset?: number
}) {
  const { token } = useAuth()
  const path = `/api/analytics/dimensions${buildQuery(params)}`
  return useSWR(
    token ? [path, token] : null,
    ([p, t]) => getJson<AnalyticsDimension>(p, t),
    { keepPreviousData: true },
  )
}

export function useAnalyticsBans(params: {
  q?: string
  limit?: number
  offset?: number
}) {
  const { token } = useAuth()
  const path = `/api/analytics/bans${buildQuery(params)}`
  return useSWR(
    token ? [path, token] : null,
    ([p, t]) => getJson<ShieldBansResponse>(p, t),
    { refreshInterval: 30000, keepPreviousData: true },
  )
}
