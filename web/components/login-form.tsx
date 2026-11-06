'use client'

import { useState } from 'react'
import { Loader2Icon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useAuth } from '@/components/auth-provider'
import { Logo } from '@/components/logo'
import { ApiError, DEMO_TOKEN, getJson } from '@/lib/api'

export function LoginForm() {
  const { login } = useAuth()
  const [value, setValue] = useState('')
  const [remember, setRemember] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    const token = value.trim()
    if (!token) return
    setPending(true)
    setError('')
    try {
      await getJson('/api/sites', token)
      login(token, remember)
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) setError('Token 无效')
      else if (err instanceof ApiError) setError(`请求失败（${err.status}）`)
      else setError('无法连接到服务')
    } finally {
      setPending(false)
    }
  }

  return (
    <div className="flex min-h-dvh items-center justify-center px-4">
      <form onSubmit={submit} className="w-full max-w-sm space-y-6">
        <div className="flex flex-col items-center gap-4">
          <Logo className="size-14 rounded-2xl p-[14px] [&>span]:w-1.5" />
          <h1 className="text-xl font-semibold tracking-tight">部署控制台</h1>
        </div>

        <div className="space-y-2">
          <Label htmlFor="token" className="sr-only">
            访问 Token
          </Label>
          <Input
            id="token"
            type="password"
            autoComplete="current-password"
            autoFocus
            placeholder="访问 Token"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            aria-invalid={!!error}
            className="h-11 text-center font-mono"
          />
          <p
            role="alert"
            className="h-4 text-center text-xs text-destructive"
          >
            {error}
          </p>
        </div>

        <div className="space-y-3">
          <Button type="submit" size="lg" className="h-11 w-full" disabled={pending || !value.trim()}>
            {pending && <Loader2Icon className="animate-spin" />}
            进入
          </Button>
          <label className="flex cursor-pointer items-center justify-center gap-2 text-xs text-muted-foreground">
            <input
              type="checkbox"
              checked={remember}
              onChange={(e) => setRemember(e.target.checked)}
              className="size-3.5 accent-foreground"
            />
            在此设备上保持登录
          </label>
        </div>

        <button
          type="button"
          onClick={() => login(DEMO_TOKEN, false)}
          className="block w-full text-center text-xs text-muted-foreground/70 underline-offset-4 hover:text-foreground hover:underline"
        >
          查看演示数据
        </button>
      </form>
    </div>
  )
}
