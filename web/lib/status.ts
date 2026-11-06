import {
  CheckIcon,
  ChevronsRightIcon,
  ClockIcon,
  LoaderCircleIcon,
  XIcon,
  type LucideIcon,
} from 'lucide-react'
import type { DeployStatus } from './types'

interface StatusMeta {
  label: string
  hint: string
  icon: LucideIcon
  text: string
  bg: string
  soft: string
  ring: string
  pulse: boolean
}

export const STATUS: Record<DeployStatus, StatusMeta> = {
  queued: {
    label: '排队中',
    hint: '任务已入队，等待执行器处理',
    icon: ClockIcon,
    text: 'text-wait',
    bg: 'bg-wait',
    soft: 'bg-wait/12',
    ring: 'ring-wait/30',
    pulse: true,
  },
  running: {
    label: '执行中',
    hint: '执行器正在运行站点部署脚本',
    icon: LoaderCircleIcon,
    text: 'text-run',
    bg: 'bg-run',
    soft: 'bg-run/12',
    ring: 'ring-run/30',
    pulse: true,
  },
  succeeded: {
    label: '成功',
    hint: '部署脚本返回成功',
    icon: CheckIcon,
    text: 'text-ok',
    bg: 'bg-ok',
    soft: 'bg-ok/12',
    ring: 'ring-ok/30',
    pulse: false,
  },
  failed: {
    label: '失败',
    hint: '部署失败或被中断',
    icon: XIcon,
    text: 'text-bad',
    bg: 'bg-bad',
    soft: 'bg-bad/12',
    ring: 'ring-bad/30',
    pulse: false,
  },
  superseded: {
    label: '已替代',
    hint: '被后来接收的同分支推送替代',
    icon: ChevronsRightIcon,
    text: 'text-gone',
    bg: 'bg-gone',
    soft: 'bg-gone/15',
    ring: 'ring-gone/30',
    pulse: false,
  },
}

export const STATUS_ORDER: DeployStatus[] = [
  'queued',
  'running',
  'succeeded',
  'failed',
  'superseded',
]

export function isActive(status: DeployStatus) {
  return status === 'queued' || status === 'running'
}
