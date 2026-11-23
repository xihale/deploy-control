import Link from 'next/link'
import { ChevronRightIcon, GitCommitHorizontalIcon, TagIcon, UserIcon } from 'lucide-react'
import { StatusBadge } from '@/components/status-badge'
import { durationBetween, shortSha, timeAgo } from '@/lib/format'
import { isActive } from '@/lib/status'
import type { DeployJob } from '@/lib/types'

export function DeployRow({
  job,
  showSite = false,
}: {
  job: DeployJob
  showSite?: boolean
}) {
  const sha = shortSha(job.checked_sha ?? job.sha)
  const duration = durationBetween(
    job.started_at,
    isActive(job.status) ? undefined : job.finished_at,
  )
  const TriggerIcon =
    job.trigger_type === 'manual'
      ? UserIcon
      : job.trigger_type === 'release'
        ? TagIcon
        : GitCommitHorizontalIcon
  const triggerLabel =
    job.trigger_type === 'manual'
      ? '手动触发'
      : job.trigger_type === 'release'
        ? '发布触发'
        : '推送触发'

  return (
    <Link
      href={`/deploy?id=${job.id}`}
      className="group surface flex items-center gap-3 rounded-xl border bg-card px-3.5 py-3 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring sm:gap-4 sm:px-4 sm:py-3.5"
    >
      <StatusBadge status={job.status} className="w-[4.75rem] shrink-0 justify-center sm:w-[5.5rem]" />
      <div className="min-w-0 flex-1 space-y-1 sm:flex sm:items-center sm:gap-3 sm:space-y-0">
        <div className="flex min-w-0 items-center gap-2 sm:shrink-0">
          {showSite && (
            <span className="max-w-[9.5rem] truncate rounded-md bg-muted px-1.5 py-0.5 font-mono text-xs text-muted-foreground sm:max-w-none sm:shrink-0">
              {job.site}
            </span>
          )}
          <span className="shrink-0 font-mono text-sm font-medium tabular-nums">#{job.id}</span>
        </div>
        <div className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground sm:flex-1 sm:gap-3 sm:text-sm">
          <span
            className="flex shrink-0 items-center text-muted-foreground"
            title={triggerLabel}
          >
            <TriggerIcon className="size-3.5 sm:size-4" />
            <span className="sr-only">{triggerLabel}</span>
          </span>
          <span className="truncate font-mono">
            {sha ?? job.branch}
          </span>
          {duration && (
            <span className="shrink-0 font-mono text-[11px] text-muted-foreground/80 tabular-nums sm:hidden">
              · {duration}
            </span>
          )}
        </div>
      </div>
      <span className="hidden shrink-0 font-mono text-xs text-muted-foreground tabular-nums sm:block">
        {duration ?? ''}
      </span>
      <span className="shrink-0 whitespace-nowrap text-right text-xs text-muted-foreground sm:w-16">
        {timeAgo(job.created_at)}
      </span>
      <ChevronRightIcon className="size-4 shrink-0 text-muted-foreground/50 transition-transform duration-200 ease-[cubic-bezier(0.16,1,0.3,1)] group-hover:translate-x-0.5" />
    </Link>
  )
}
