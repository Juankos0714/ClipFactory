/** Helpers puros de formato (fechas RFC3339 UTC, duraciones, números). */

const TIME_FORMAT = new Intl.DateTimeFormat('es', {
  dateStyle: 'short',
  timeStyle: 'short',
  timeZone: 'UTC',
})

const COMPACT = new Intl.NumberFormat('es', { notation: 'compact', maximumFractionDigits: 1 })

/** RFC3339 UTC → fecha/hora corta en es. null/'' → em dash. */
export function formatDateTime(value: string | null | undefined): string {
  if (!value) return '—'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return '—'
  return TIME_FORMAT.format(d)
}

/** Segundos → mm:ss (o h:mm:ss si ≥1h). null → em dash. */
export function formatDuration(seconds: number | null | undefined): string {
  if (seconds == null || Number.isNaN(seconds)) return '—'
  const total = Math.round(seconds)
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = total % 60
  const mm = String(m).padStart(2, '0')
  const ss = String(s).padStart(2, '0')
  return h > 0 ? `${h}:${mm}:${ss}` : `${mm}:${ss}`
}

/** Número → notación compacta (1.234 → "1,2 k"). */
export function formatNumber(n: number | null | undefined): string {
  if (n == null || Number.isNaN(n)) return '—'
  return COMPACT.format(n)
}