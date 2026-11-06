const rtf = new Intl.RelativeTimeFormat('zh-CN', { numeric: 'auto' })

export function timeAgo(iso?: string) {
  if (!iso) return '—'
  const diff = (new Date(iso).getTime() - Date.now()) / 1000
  const abs = Math.abs(diff)
  if (abs < 45) return '刚刚'
  if (abs < 3600) return rtf.format(Math.round(diff / 60), 'minute')
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), 'hour')
  if (abs < 86400 * 30) return rtf.format(Math.round(diff / 86400), 'day')
  return new Date(iso).toLocaleDateString('zh-CN')
}

export function fullTime(iso?: string) {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

export function clockTime(iso?: string) {
  if (!iso) return '—'
  return new Date(iso).toLocaleTimeString('zh-CN', { hour12: false })
}

export function durationBetween(from?: string, to?: string) {
  if (!from) return null
  const end = to ? new Date(to).getTime() : Date.now()
  const s = Math.max(0, Math.round((end - new Date(from).getTime()) / 1000))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${s % 60}s`
  return `${Math.floor(m / 60)}h ${m % 60}m`
}

export function shortSha(sha?: string) {
  return sha ? sha.slice(0, 7) : null
}

export function repoLabel(repo: string) {
  return repo
    .replace(/^https?:\/\//, '')
    .replace(/^github\.com\//, '')
    .replace(/\.git$/, '')
}

export function repoUrl(repo: string) {
  return /^https?:\/\//.test(repo) ? repo.replace(/\.git$/, '') : undefined
}
