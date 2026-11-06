import { cn } from '@/lib/utils'

export function Logo({ className }: { className?: string }) {
  return (
    <span
      aria-hidden
      className={cn(
        'inline-flex items-end justify-center gap-[3px] rounded-lg bg-logo-tile p-[7px]',
        className,
      )}
    >
      <span className="h-2/5 w-[3px] rounded-full bg-white/60" />
      <span className="h-3/5 w-[3px] rounded-full bg-white/80" />
      <span className="h-full w-[3px] rounded-full bg-ok" />
    </span>
  )
}
