'use client'

import { useEffect, useState } from 'react'
import Link from 'next/link'
import { useSearchParams } from 'next/navigation'
import {
  ArrowLeftIcon,
  CheckIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  CopyIcon,
  ExternalLinkIcon,
  GitBranchIcon,
  InboxIcon,
  WebhookIcon,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { DeployRow } from '@/components/deploy-row'
import { ManualDeployDialog } from '@/components/manual-deploy-dialog'
import { PulseStrip } from '@/components/pulse-strip'
import { ErrorState } from '@/components/states'
import { StatusDot } from '@/components/status-badge'
import { useDeploys, useSites } from '@/hooks/use-api'
import { repoLabel, repoUrl } from '@/lib/format'
import { STATUS, STATUS_ORDER } from '@/lib/status'
import { cn } from '@/lib/utils'
import type { DeployStatus, Site } from '@/lib/types'

const PAGE_SIZE = 15

function AccessPanel({ site }: { site: Site }) {
  const [origin, setOrigin] = useState('')
  const [copied, setCopied] = useState(false)

  useEffect(() => setOrigin(window.location.origin), [])

  const endpoint = origin ? `${origin}/webhook/github/${site.name}` : ''

  async function copy() {
    if (!endpoint) return
    await navigator.clipboard.writeText(endpoint)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  return (
    <details className="group rounded-2xl border bg-card px-5 py-4">
      <summary className="flex cursor-pointer list-none items-center gap-2 text-sm text-muted-foreground hover:text-foreground [&::-webkit-details-marker]:hidden">
        <WebhookIcon className="size-4" />
        接入信息
        <span
          className={cn(
            'rounded-full px-2 py-0.5 text-xs',
            site.has_webhook_secret
              ? 'bg-ok/12 text-ok'
              : 'bg-wait/15 text-wait',
          )}
        >
          {site.has_webhook_secret ? '密钥已配置' : '缺少密钥'}
        </span>
        <span className="ml-auto text-xs text-muted-foreground/70 group-open:hidden">
          展开
        </span>
      </summary>

      <dl className="mt-4 space-y-3 border-t pt-4 text-sm">
        <div className="flex flex-wrap items-center gap-2">
          <dt className="w-20 shrink-0 text-xs text-muted-foreground">
            Webhook
          </dt>
          <dd className="flex min-w-0 flex-1 items-center gap-2">
            <code className="truncate font-mono text-xs">
              {endpoint || '—'}
            </code>
            <Button
              variant="ghost"
              size="icon-xs"
              onClick={copy}
              disabled={!endpoint}
              aria-label="复制 Webhook 地址"
              title="复制 Webhook 地址"
            >
              {copied ? <CheckIcon /> : <CopyIcon />}
            </Button>
          </dd>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <dt className="w-20 shrink-0 text-xs text-muted-foreground">脚本</dt>
          <dd className="min-w-0 flex-1 truncate font-mono text-xs">
            {site.script}
          </dd>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <dt className="w-20 shrink-0 text-xs text-muted-foreground">
            运行用户
          </dt>
          <dd className="font-mono text-xs">{site.run_as ?? '服务用户'}</dd>
        </div>
      </dl>
      <p className="mt-3 text-xs text-muted-foreground">
        在 GitHub 仓库的 Webhook 中填写该地址，事件选择 push，并配置与此站点一致的密钥。
      </p>
    </details>
  )
}

export function SiteView() {
  const name = useSearchParams().get('name') ?? ''
  const sites = useSites()
  const [filter, setFilter] = useState<DeployStatus | ''>('')
  const [page, setPage] = useState(0)

  const site = sites.data?.find((s) => s.name === name)
  const recent = useDeploys({ site: name, limit: 16 })
  const list = useDeploys({
    site: name,
    status: filter,
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
  })

  if (sites.error) return <ErrorState error={sites.error} onRetry={() => sites.mutate()} />
  if (sites.data && !site) {
    return (
      <div className="space-y-6">
        <BackLink />
        <ErrorState error={{ status: 404 }} />
      </div>
    )
  }

  const total = list.data?.total ?? 0
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const href = site ? repoUrl(site.repo) : undefined

  return (
    <div className="space-y-8">
      <BackLink />

      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-2">
          <h1 className="text-3xl font-semibold tracking-tight">{name}</h1>
          {site && (
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-muted-foreground">
              <span className="flex items-center gap-1.5 font-mono">
                <GitBranchIcon className="size-3.5" />
                {site.branch}
              </span>
              {href ? (
                <a
                  href={href}
                  target="_blank"
                  rel="noreferrer"
                  className="flex items-center gap-1.5 font-mono hover:text-foreground"
                >
                  {repoLabel(site.repo)}
                  <ExternalLinkIcon className="size-3" />
                </a>
              ) : (
                <span className="font-mono">{repoLabel(site.repo)}</span>
              )}
            </div>
          )}
        </div>
        {site && <ManualDeployDialog site={site} />}
      </div>

      <div className="rounded-2xl border bg-card p-5">
        <PulseStrip jobs={recent.data?.jobs ?? []} slots={16} className="h-10 [&>span:last-child]:h-10 [&>span]:h-7" />
      </div>

      {site && <AccessPanel site={site} />}

      <section className="space-y-4">
        <div
          role="tablist"
          aria-label="按状态筛选"
          className="flex flex-wrap gap-1.5"
        >
          {(['', ...STATUS_ORDER] as const).map((s) => {
            const active = filter === s
            return (
              <button
                key={s || 'all'}
                role="tab"
                aria-selected={active}
                onClick={() => {
                  setFilter(s)
                  setPage(0)
                }}
                className={cn(
                  'flex items-center gap-2 rounded-full px-3.5 py-1.5 text-sm transition-colors',
                  active
                    ? 'bg-foreground text-background'
                    : 'text-muted-foreground hover:bg-muted hover:text-foreground',
                )}
              >
                {s && <StatusDot status={s} className="size-2" />}
                {s ? STATUS[s].label : '全部'}
              </button>
            )
          })}
        </div>

        {list.error ? (
          <ErrorState error={list.error} onRetry={() => list.mutate()} />
        ) : !list.data ? (
          <div className="space-y-2">
            {Array.from({ length: 4 }).map((_, i) => (
              <div key={i} className="h-16 animate-pulse rounded-xl bg-muted" />
            ))}
          </div>
        ) : list.data.jobs.length === 0 ? (
          <div className="flex flex-col items-center gap-3 py-20 text-muted-foreground">
            <InboxIcon className="size-9" strokeWidth={1.25} />
            <p className="text-sm">暂无部署记录</p>
          </div>
        ) : (
          <ul className="space-y-2">
            {list.data.jobs.map((job) => (
              <li key={job.id}>
                <DeployRow job={job} />
              </li>
            ))}
          </ul>
        )}

        {pages > 1 && (
          <div className="flex items-center justify-center gap-3 pt-2">
            <Button
              variant="ghost"
              size="icon"
              disabled={page === 0}
              onClick={() => setPage((p) => p - 1)}
              aria-label="上一页"
            >
              <ChevronLeftIcon />
            </Button>
            <span className="font-mono text-sm text-muted-foreground tabular-nums">
              {page + 1} / {pages}
            </span>
            <Button
              variant="ghost"
              size="icon"
              disabled={page + 1 >= pages}
              onClick={() => setPage((p) => p + 1)}
              aria-label="下一页"
            >
              <ChevronRightIcon />
            </Button>
          </div>
        )}
      </section>
    </div>
  )
}

function BackLink() {
  return (
    <Link
      href="/"
      className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
    >
      <ArrowLeftIcon className="size-4" />
      站点
    </Link>
  )
}
