import { cn } from '@/lib/utils'
import { STATUS } from '@/lib/status'
import type { DeployStatus } from '@/lib/types'

export function StatusDot({
  status,
  className,
}: {
  status: DeployStatus
  className?: string
}) {
  const meta = STATUS[status]
  return (
    <span
      aria-hidden
      className={cn(
        'inline-block size-2.5 shrink-0 rounded-full',
        meta.bg,
        meta.text,
        meta.pulse && 'status-pulse',
        className,
      )}
    />
  )
}

export function StatusBadge({
  status,
  className,
}: {
  status: DeployStatus
  className?: string
}) {
  const meta = STATUS[status]
  const Icon = meta.icon
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium',
        meta.soft,
        meta.text,
        className,
      )}
    >
      <Icon
        className={cn('size-3.5', status === 'running' && 'animate-spin')}
        strokeWidth={2.5}
      />
      {meta.label}
    </span>
  )
}
