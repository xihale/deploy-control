'use client'

import Link from 'next/link'
import { usePathname } from 'next/navigation'
import { BarChart3Icon, LogOutIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useAuth } from '@/components/auth-provider'
import { LoginForm } from '@/components/login-form'
import { Logo } from '@/components/logo'
import { useHealth, useMe } from '@/hooks/use-api'
import { cn } from '@/lib/utils'

function HealthPill() {
  const { data, error } = useHealth()
  const offline = !!error
  const busy = (data?.running ?? 0) + (data?.queued ?? 0)

  return (
    <div
      className="flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full bg-muted px-2.5 py-1 text-xs text-muted-foreground sm:gap-2 sm:px-3 sm:py-1.5"
      title={offline ? 'API 无响应' : 'API 正常'}
    >
      <span
        className={cn(
          'size-2 rounded-full',
          offline ? 'bg-bad' : 'bg-ok',
          !offline && busy > 0 && 'status-pulse text-ok',
        )}
      />
      {offline ? (
        '离线'
      ) : (
        <span className="font-mono tabular-nums">
          {data?.running ?? 0} / {data?.queued ?? 0}
        </span>
      )}
      <span className="sr-only">执行中 / 排队</span>
    </div>
  )
}

export function AppShell({ children }: { children: React.ReactNode }) {
  const { token, ready, isDemo, logout } = useAuth()
  const me = useMe()
  const pathname = usePathname()

  if (!ready) return <div className="min-h-dvh" />
  if (!token) return <LoginForm />

  const isAnalytics = pathname?.startsWith('/analytics') ?? false

  return (
    <div className="min-h-dvh">
      <header className="sticky top-0 z-20 border-b bg-background/80 backdrop-blur-md">
        <div className="mx-auto flex h-14 max-w-5xl items-center justify-between gap-2 px-3 sm:gap-3 sm:px-6">
          <div className="flex min-w-0 items-center gap-2 sm:gap-5">
            <Link href="/" className="flex shrink-0 items-center gap-2.5">
              <Logo className="size-7 shrink-0" />
              <span className="hidden whitespace-nowrap text-sm font-semibold tracking-tight sm:inline">
                部署控制台
              </span>
            </Link>
            <nav aria-label="主导航" className="flex shrink-0 items-center gap-1 text-xs">
              <Link
                href="/"
                className={cn(
                  'shrink-0 whitespace-nowrap rounded-full px-2.5 py-1.5 transition-colors sm:px-3',
                  !isAnalytics
                    ? 'bg-muted font-medium text-foreground'
                    : 'text-muted-foreground hover:text-foreground',
                )}
              >
                总览
              </Link>
              <Link
                href="/analytics"
                className={cn(
                  'inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-full px-2.5 py-1.5 transition-colors sm:px-3',
                  isAnalytics
                    ? 'bg-muted font-medium text-foreground'
                    : 'text-muted-foreground hover:text-foreground',
                )}
              >
                <BarChart3Icon className="size-3.5 shrink-0" />
                访问统计
              </Link>
            </nav>
          </div>
          <div className="flex shrink-0 items-center gap-1.5 sm:gap-2">
            {isDemo && (
              <span className="shrink-0 whitespace-nowrap rounded-full bg-wait/15 px-2 py-0.5 text-[11px] font-medium text-wait sm:px-2.5 sm:py-1 sm:text-xs">
                演示数据
              </span>
            )}
            {me.data && (
              <span
                className="hidden items-center gap-1.5 text-xs text-muted-foreground sm:flex"
                title={`${me.data.name} · ${me.data.role === 'admin' ? '管理员' : '只读'}`}
              >
                {me.data.name}
                <span className="rounded-full bg-muted px-1.5 py-0.5">
                  {me.data.role === 'admin' ? '管理员' : '只读'}
                </span>
              </span>
            )}
            <HealthPill />
            <Button
              variant="ghost"
              size="icon"
              onClick={logout}
              aria-label="退出登录"
              title="退出登录"
              className="shrink-0"
            >
              <LogOutIcon />
            </Button>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-5xl px-3.5 py-5 sm:px-6 sm:py-10">
        {children}
      </main>
    </div>
  )
}
