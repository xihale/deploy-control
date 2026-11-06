import { cn } from '@/lib/utils'
import { STATUS } from '@/lib/status'
import type { DeployJob } from '@/lib/types'

// jobs 按时间从新到旧传入，条带从左到右为旧 → 新
export function PulseStrip({
  jobs,
  slots = 16,
  className,
}: {
  jobs: DeployJob[]
  slots?: number
  className?: string
}) {
  const recent = jobs.slice(0, slots).reverse()
  const empty = Math.max(0, slots - recent.length)

  return (
    <div
      className={cn('flex h-8 items-end gap-[3px]', className)}
      role="img"
      aria-label={`最近 ${recent.length} 次部署状态`}
    >
      {Array.from({ length: empty }).map((_, i) => (
        <span key={`e${i}`} className="h-3 flex-1 rounded-full bg-muted" />
      ))}
      {recent.map((job, i) => {
        const meta = STATUS[job.status]
        const latest = i === recent.length - 1
        return (
          <span
            key={job.id}
            title={`#${job.id} · ${meta.label}`}
            className={cn(
              'flex-1 rounded-full transition-all',
              meta.bg,
              latest ? 'h-8' : 'h-5 opacity-70',
              meta.pulse && 'animate-pulse',
            )}
          />
        )
      })}
    </div>
  )
}
