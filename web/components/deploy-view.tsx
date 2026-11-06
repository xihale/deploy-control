'use client'

import { useState } from 'react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import {
  ArrowLeftIcon,
  GitBranchIcon,
  GitCommitHorizontalIcon,
  Loader2Icon,
  RocketIcon,
  UserIcon,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useAuth } from '@/components/auth-provider'
import { LogViewer } from '@/components/log-viewer'
import { ErrorState } from '@/components/states'
import { StatusBadge } from '@/components/status-badge'
import { useDeploy, useMe } from '@/hooks/use-api'
import { ApiError, postJson } from '@/lib/api'
import { clockTime, durationBetween, fullTime, shortSha } from '@/lib/format'
import { STATUS, isActive } from '@/lib/status'
import { cn } from '@/lib/utils'
import type { DeployJob } from '@/lib/types'

function Fact({
  label,
  children,
  mono = true,
}: {
  label: string
  children: React.ReactNode
  mono?: boolean
}) {
  return (
    <div className="space-y-1">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className={cn('truncate text-sm', mono && 'font-mono')}>{children}</dd>
    </div>
  )
}

function Timeline({ job }: { job: DeployJob }) {
  const steps = [
    { label: '入队', at: job.created_at },
    { label: '开始', at: job.started_at },
    { label: '结束', at: job.finished_at },
  ]
  const reached = steps.filter((s) => s.at).length
  const failed = job.status === 'failed'

  return (
    <ol className="flex items-center" aria-label="时间线">
      {steps.map((s, i) => {
        const done = !!s.at
        const last = i === steps.length - 1
        const color = done ? (last && failed ? 'bg-bad' : last ? 'bg-ok' : 'bg-foreground') : 'bg-muted'
        return (
          <li key={s.label} className="flex flex-1 items-center last:flex-none">
            <div className="flex flex-col items-start gap-1.5">
              <span
                className={cn(
                  'size-3 rounded-full',
                  color,
                  !done && 'border border-dashed border-muted-foreground/40',
                  i === reached - 1 && isActive(job.status) && 'status-pulse text-run',
                )}
              />
              <span className="text-xs text-muted-foreground">{s.label}</span>
              <span className="h-4 font-mono text-xs tabular-nums" title={fullTime(s.at)}>
                {s.at ? clockTime(s.at) : ''}
              </span>
            </div>
            {!last && (
              <span
                className={cn(
                  'mx-3 mb-9 h-0.5 flex-1 rounded-full',
                  steps[i + 1].at ? 'bg-foreground/80' : 'bg-muted',
                )}
              />
            )}
          </li>
        )
      })}
    </ol>
  )
}

function RedeployButton({ job }: { job: DeployJob }) {
  const { token } = useAuth()
  const me = useMe()
  const router = useRouter()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')

  if (me.data?.role !== 'admin') return null

  async function redeploy() {
    setPending(true)
    setError('')
    try {
      const next = await postJson<DeployJob>('/api/deploys', token, {
        site: job.site,
        branch: job.branch,
        ...(job.checked_sha && { sha: job.checked_sha }),
      })
      router.push(`/deploy?id=${next.id}`)
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : '无法连接到服务',
      )
    } finally {
      setPending(false)
    }
  }

  return (
    <div className="flex flex-col items-end gap-1">
      <Button
        variant="outline"
        size="lg"
        onClick={redeploy}
        disabled={pending}
        className="h-9 px-4"
      >
        {pending ? <Loader2Icon className="animate-spin" /> : <RocketIcon />}
        重新部署
      </Button>
      {error && (
        <span role="alert" className="max-w-56 text-right text-xs text-bad">
          {error}
        </span>
      )}
    </div>
  )
}

export function DeployView() {
  const id = Number(useSearchParams().get('id')) || null
  const { data: job, error, mutate } = useDeploy(id)

  if (!id) return <ErrorState error={{ status: 404 }} />
  if (error) return <ErrorState error={error} onRetry={() => mutate()} />
  if (!job) {
    return (
      <div className="space-y-6" aria-busy="true">
        <div className="h-9 w-48 animate-pulse rounded-lg bg-muted" />
        <div className="h-32 animate-pulse rounded-2xl bg-muted" />
        <div className="h-64 animate-pulse rounded-2xl bg-muted" />
      </div>
    )
  }

  const active = isActive(job.status)
  const duration = durationBetween(job.started_at, active ? undefined : job.finished_at)

  return (
    <div className="space-y-8">
      <Link
        href={`/site?name=${encodeURIComponent(job.site)}`}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeftIcon className="size-4" />
        {job.site}
      </Link>

      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-3">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
          <h1 className="font-mono text-3xl font-semibold tracking-tight tabular-nums">
            #{job.id}
          </h1>
          <StatusBadge status={job.status} className="px-3 py-1.5 text-sm" />
          {duration && (
            <span className="font-mono text-sm text-muted-foreground tabular-nums">
              {duration}
            </span>
          )}
        </div>
        <RedeployButton job={job} />
      </div>

      {job.status === 'failed' && job.error && (
        <p
          role="alert"
          className="rounded-xl bg-bad/10 px-4 py-3 font-mono text-sm text-bad"
        >
          {job.error}
          {job.exit_code !== undefined && (
            <span className="ml-2 opacity-70">exit {job.exit_code}</span>
          )}
        </p>
      )}
      {job.status === 'superseded' && (
        <p className="rounded-xl bg-gone/10 px-4 py-3 text-sm text-muted-foreground">
          {STATUS.superseded.hint}
        </p>
      )}

      <div className="space-y-8 rounded-2xl border bg-card p-6">
        <Timeline job={job} />
        <dl className="grid grid-cols-2 gap-x-6 gap-y-5 border-t pt-6 sm:grid-cols-4">
          <Fact label="触发">
            <span className="flex items-center gap-1.5 font-sans">
              {job.trigger_type === 'manual' ? (
                <UserIcon className="size-3.5" />
              ) : (
                <GitCommitHorizontalIcon className="size-3.5" />
              )}
              {job.trigger_type === 'manual' ? '手动' : '推送'}
            </span>
          </Fact>
          <Fact label="分支">
            <span className="flex items-center gap-1.5">
              <GitBranchIcon className="size-3.5 text-muted-foreground" />
              {job.branch}
            </span>
          </Fact>
          <Fact label="检出提交">
            <span title={job.checked_sha ?? job.sha}>
              {shortSha(job.checked_sha ?? job.sha) ?? '—'}
            </span>
            {job.sha && job.sha !== job.checked_sha && (
              <span
                className="block text-xs text-muted-foreground"
                title={job.sha}
              >
                请求 {shortSha(job.sha)}
              </span>
            )}
          </Fact>
          <Fact label="尝试">{job.attempts}</Fact>
        </dl>
      </div>

      <LogViewer job={job} />
    </div>
  )
}
