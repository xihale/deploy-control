'use client'

import useSWR from 'swr'
import { useAuth } from '@/components/auth-provider'
import { buildQuery, getJson, getText } from '@/lib/api'
import { isActive } from '@/lib/status'
import type { DeployJob, DeployList, DeployStatus, Health, Me, Site } from '@/lib/types'

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
