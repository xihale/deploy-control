import Link from 'next/link'
import { ChevronRightIcon, GitCommitHorizontalIcon, UserIcon } from 'lucide-react'
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
  const TriggerIcon = job.trigger_type === 'manual' ? UserIcon : GitCommitHorizontalIcon

  return (
    <Link
      href={`/deploy?id=${job.id}`}
      className="group flex items-center gap-4 rounded-xl border bg-card px-4 py-3.5 transition-colors hover:bg-muted/60 focus-visible:outline-2 focus-visible:outline-ring"
    >
      <StatusBadge status={job.status} className="w-[5.5rem] shrink-0 justify-center" />
      <div className="flex min-w-0 flex-1 items-center gap-3">
        {showSite && (
          <span className="shrink-0 rounded-md bg-muted px-1.5 py-0.5 font-mono text-xs text-muted-foreground">
            {job.site}
          </span>
        )}
        <span className="font-mono text-sm font-medium tabular-nums">#{job.id}</span>
        <span
          className="flex items-center gap-1.5 text-muted-foreground"
          title={job.trigger_type === 'manual' ? '手动触发' : '推送触发'}
        >
          <TriggerIcon className="size-4" />
          <span className="sr-only">
            {job.trigger_type === 'manual' ? '手动触发' : '推送触发'}
          </span>
        </span>
        <span className="truncate font-mono text-sm text-muted-foreground">
          {sha ?? job.branch}
        </span>
      </div>
      <span className="hidden font-mono text-xs text-muted-foreground tabular-nums sm:block">
        {duration ?? ''}
      </span>
      <span className="w-16 shrink-0 text-right text-xs text-muted-foreground">
        {timeAgo(job.created_at)}
      </span>
      <ChevronRightIcon className="size-4 text-muted-foreground/50 transition-transform group-hover:translate-x-0.5" />
    </Link>
  )
}
