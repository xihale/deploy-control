'use client'

import Link from 'next/link'
import { LogOutIcon } from 'lucide-react'
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
      className="flex items-center gap-2 rounded-full bg-muted px-3 py-1.5 text-xs text-muted-foreground"
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

  if (!ready) return <div className="min-h-dvh" />
  if (!token) return <LoginForm />

  return (
    <div className="min-h-dvh">
      <header className="sticky top-0 z-20 border-b bg-background/80 backdrop-blur-md">
        <div className="mx-auto flex h-14 max-w-5xl items-center justify-between gap-3 px-4 sm:px-6">
          <Link href="/" className="flex items-center gap-2.5">
            <Logo className="size-7" />
            <span className="text-sm font-semibold tracking-tight">部署控制台</span>
          </Link>
          <div className="flex items-center gap-2">
            {isDemo && (
              <span className="rounded-full bg-wait/15 px-2.5 py-1 text-xs font-medium text-wait">
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
            >
              <LogOutIcon />
            </Button>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-5xl px-4 py-8 sm:px-6 sm:py-10">
        {children}
      </main>
    </div>
  )
}
