export function formatBytes(n: number): string {
  const unit = 1024
  if (n < unit) return `${n} B`
  let div = unit
  let exp = 0
  for (let x = n / unit; x >= unit; x /= unit) {
    div *= unit
    exp++
  }
  return `${(n / div).toFixed(1)} ${'KMGTPE'[exp]}iB`
}

export function usageLevel(usedFraction: number): 'ok' | 'warn' | 'danger' {
  if (usedFraction >= 0.9) return 'danger'
  if (usedFraction >= 0.7) return 'warn'
  return 'ok'
}
