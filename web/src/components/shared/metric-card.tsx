import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

/** Card de KPI reutilizable (Dashboard y secciones de conteo). */
export function MetricCard({
  label,
  value,
  hint,
  loading = false,
  accent = 'neutral',
}: {
  label: string
  value: number | string | null
  hint?: string
  loading?: boolean
  accent?: 'neutral' | 'green' | 'amber' | 'red' | 'blue'
}) {
  const dot = {
    neutral: 'bg-neutral-500',
    green: 'bg-emerald-400',
    amber: 'bg-amber-400',
    red: 'bg-red-400',
    blue: 'bg-sky-400',
  }[accent]

  return (
    <Card className="p-4">
      <p className="flex items-center gap-2 text-xs font-medium uppercase tracking-wide text-neutral-400">
        <span aria-hidden="true" className={`h-1.5 w-1.5 rounded-full ${dot}`} />
        {label}
      </p>
      {loading ? (
        <Skeleton className="mt-2 h-7 w-20" />
      ) : (
        <p className="mt-1 text-2xl font-semibold tabular-nums text-neutral-100">{value ?? '—'}</p>
      )}
      {hint ? <p className="mt-0.5 text-xs text-neutral-500">{hint}</p> : null}
    </Card>
  )
}