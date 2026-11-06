'use client'

import { useState } from 'react'
import { useRouter } from 'next/navigation'
import { mutate } from 'swr'
import { Loader2Icon, RocketIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useAuth } from '@/components/auth-provider'
import { useMe } from '@/hooks/use-api'
import { ApiError, postJson } from '@/lib/api'
import type { DeployJob, Site } from '@/lib/types'

const SHA_RE = /^[0-9a-f]{7,40}$/i

export function ManualDeployDialog({ site }: { site: Site }) {
  const { token } = useAuth()
  const me = useMe()
  const router = useRouter()
  const [open, setOpen] = useState(false)
  const [branch, setBranch] = useState('')
  const [sha, setSha] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')

  if (me.data?.role !== 'admin') return null

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    const trimmedSha = sha.trim()
    if (trimmedSha && !SHA_RE.test(trimmedSha)) {
      setError('提交哈希格式不正确')
      return
    }
    setPending(true)
    setError('')
    try {
      const job = await postJson<DeployJob>('/api/deploys', token, {
        site: site.name,
        ...(branch.trim() && { branch: branch.trim() }),
        ...(trimmedSha && { sha: trimmedSha }),
      })
      await mutate((key) => Array.isArray(key) && String(key[0]).startsWith('/api/deploys'))
      setOpen(false)
      router.push(`/deploy?id=${job.id}`)
    } catch (err) {
      if (err instanceof ApiError && err.status === 403) {
        setError('当前 Token 为只读，无法发起部署')
      } else if (err instanceof ApiError) {
        setError(err.message)
      } else {
        setError('无法连接到服务')
      }
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) setError('')
      }}
    >
      <DialogTrigger render={<Button size="lg" className="h-9 px-4" />}>
        <RocketIcon />
        部署
      </DialogTrigger>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>部署 {site.name}</DialogTitle>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="branch">分支</Label>
            <Input
              id="branch"
              value={branch}
              onChange={(e) => setBranch(e.target.value)}
              placeholder={site.branch}
              className="font-mono"
              autoComplete="off"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sha">提交</Label>
            <Input
              id="sha"
              value={sha}
              onChange={(e) => setSha(e.target.value)}
              placeholder="最新提交"
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
            />
          </div>
          <p role="alert" className="min-h-4 text-xs text-destructive">
            {error}
          </p>
          <Button type="submit" className="h-10 w-full" disabled={pending}>
            {pending && <Loader2Icon className="animate-spin" />}
            加入队列
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  )
}
