'use client'

import { useMemo } from 'react'
import Link from 'next/link'
import { GitBranchIcon, InboxIcon, ServerOffIcon } from 'lucide-react'
import { DeployRow } from '@/components/deploy-row'
import { PulseStrip } from '@/components/pulse-strip'
import { StatusDot } from '@/components/status-badge'
import { ErrorState, PageSkeleton } from '@/components/states'
import { useDeploys, useSites } from '@/hooks/use-api'
import { repoLabel, shortSha, timeAgo } from '@/lib/format'
import { STATUS, STATUS_ORDER } from '@/lib/status'
import { cn } from '@/lib/utils'
import type { DeployJob, DeployStatus, Site } from '@/lib/types'

function SiteCard({ site, jobs }: { site: Site; jobs: DeployJob[] }) {
  const latest = jobs[0]
  const meta = latest ? STATUS[latest.status] : null
  const sha = shortSha(latest?.checked_sha ?? latest?.sha)

  return (
    <Link
      href={`/site?name=${encodeURIComponent(site.name)}`}
      className={cn(
        'group surface relative flex flex-col gap-5 rounded-2xl border bg-card p-5',
        'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring',
        latest?.status === 'failed' && 'border-bad/40',
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-3">
          {latest ? (
            <StatusDot status={latest.status} className="size-3" />
          ) : (
            <span className="size-3 rounded-full border-2 border-dashed border-muted-foreground/40" />
          )}
          <h2 className="truncate text-base font-semibold tracking-tight">
            {site.name}
          </h2>
        </div>
        <span className="flex shrink-0 items-center gap-1 rounded-md bg-muted px-2 py-1 font-mono text-xs text-muted-foreground">
          <GitBranchIcon className="size-3" />
          {site.branch}
        </span>
      </div>

      <PulseStrip jobs={jobs} />

      <div className="flex items-center justify-between gap-3 text-xs">
        <span className={cn('font-medium', meta ? meta.text : 'text-muted-foreground')}>
          {meta ? meta.label : '暂无部署'}
          {latest && (
            <span className="ml-1.5 font-normal text-muted-foreground">
              {timeAgo(latest.finished_at ?? latest.started_at ?? latest.created_at)}
            </span>
          )}
        </span>
        <span className="truncate font-mono text-muted-foreground transition-colors duration-200 group-hover:text-foreground/80">
          {sha ?? repoLabel(site.repo)}
        </span>
      </div>
    </Link>
  )
}

function HealthBar({
  sites,
  latestBySite,
}: {
  sites: Site[]
  latestBySite: Map<string, DeployJob>
}) {
  const counts = useMemo(() => {
    const c: Partial<Record<DeployStatus, number>> = {}
    for (const s of sites) {
      const j = latestBySite.get(s.name)
      if (j) c[j.status] = (c[j.status] ?? 0) + 1
    }
    return c
  }, [sites, latestBySite])

  return (
    <div className="space-y-4">
      <div className="flex h-2.5 gap-1" role="img" aria-label="各站点最近一次部署状态">
        {sites.map((s) => {
          const j = latestBySite.get(s.name)
          return (
            <span
              key={s.name}
              title={`${s.name} · ${j ? STATUS[j.status].label : '暂无部署'}`}
              className={cn(
                'flex-1 rounded-full',
                j ? STATUS[j.status].bg : 'bg-muted',
                j && STATUS[j.status].pulse && 'animate-pulse',
              )}
            />
          )
        })}
      </div>
      <ul className="flex flex-wrap gap-x-5 gap-y-1.5 text-sm">
        {STATUS_ORDER.filter((s) => counts[s]).map((s) => (
          <li key={s} className="flex items-center gap-2">
            <StatusDot status={s} className="size-2" />
            <span className="font-mono font-medium tabular-nums">{counts[s]}</span>
            <span className="text-muted-foreground">{STATUS[s].label}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}

export function Overview() {
  const sites = useSites()
  const deploys = useDeploys({ limit: 100 })

  const jobsBySite = useMemo(() => {
    const map = new Map<string, DeployJob[]>()
    const sorted = [...(deploys.data?.jobs ?? [])].sort((a, b) => b.id - a.id)
    for (const j of sorted) {
      const list = map.get(j.site) ?? []
      list.push(j)
      map.set(j.site, list)
    }
    return map
  }, [deploys.data])

  const latestBySite = useMemo(() => {
    const map = new Map<string, DeployJob>()
    jobsBySite.forEach((list, k) => map.set(k, list[0]))
    return map
  }, [jobsBySite])

  const recent = useMemo(
    () => [...(deploys.data?.jobs ?? [])].sort((a, b) => b.id - a.id).slice(0, 8),
    [deploys.data],
  )

  if (sites.error) return <ErrorState error={sites.error} onRetry={() => sites.mutate()} />
  if (!sites.data) return <PageSkeleton />

  if (sites.data.length === 0) {
    return (
      <div className="flex flex-col items-center gap-4 py-24 text-muted-foreground">
        <ServerOffIcon className="size-10" strokeWidth={1.25} />
        <p className="text-sm">尚未配置站点</p>
      </div>
    )
  }

  return (
    <div className="space-y-8">
      <div className="space-y-5">
        <h1 className="flex items-baseline gap-3 text-3xl font-semibold tracking-tight">
          站点
          <span className="font-mono text-xl font-normal text-muted-foreground tabular-nums">
            {sites.data.length}
          </span>
        </h1>
        <HealthBar sites={sites.data} latestBySite={latestBySite} />
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {sites.data.map((site) => (
          <SiteCard key={site.name} site={site} jobs={jobsBySite.get(site.name) ?? []} />
        ))}
      </div>

      <section className="space-y-3">
        <div className="flex items-baseline justify-between gap-3">
          <h2 className="text-lg font-semibold tracking-tight">最近部署</h2>
          <span className="text-xs text-muted-foreground">全部站点</span>
        </div>
        {recent.length === 0 ? (
          <div className="flex flex-col items-center gap-3 rounded-2xl border border-dashed py-14 text-muted-foreground">
            <InboxIcon className="size-9" strokeWidth={1.25} />
            <p className="text-sm">还没有部署记录</p>
          </div>
        ) : (
          <ul className="space-y-2">
            {recent.map((job) => (
              <li key={job.id}>
                <DeployRow job={job} showSite />
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}
