import { useJobsList } from '@/hooks/use-jobs'
import { StatusBadge } from '@/components/ui/badge'
import { Card, CardHeader } from '@/components/ui/card'
import { StateView } from '@/components/ui/state'

export function ProductionQueue() {
  const { data, isLoading, error, refetch } = useJobsList({ pageSize: 5 })

  return (
    <Card>
      <CardHeader id="queue" title="Trabajos recientes" />
      <StateView
        isLoading={isLoading}
        error={error}
        onRetry={() => void refetch()}
        isEmpty={(data?.data.length ?? 0) === 0}
        emptyTitle="Sin trabajos recientes"
        emptyBody="La cola aparecerá acá cuando haya jobs activos."
        loadingRows={4}
      >
        {data ? (
          <ul className="divide-y divide-border">
            {data.data.map((job) => (
              <li key={job.id} className="flex items-center justify-between gap-3 px-4 py-2.5">
                <div>
                  <p className="text-sm font-medium text-neutral-200">{job.type}</p>
                  <p className="text-xs text-neutral-500">
                    ref #{job.reference_type}:{job.reference_id} · intento {job.attempts}
                  </p>
                </div>
                <StatusBadge status={job.status} />
              </li>
            ))}
          </ul>
        ) : null}
      </StateView>
    </Card>
  )
}