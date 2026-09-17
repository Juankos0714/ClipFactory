import { useHealth } from '@/hooks/use-system'
import { Badge } from '@/components/ui/badge'
import { Card, CardHeader } from '@/components/ui/card'
import { StateView } from '@/components/ui/state'

export function SystemStatus() {
  const { data, isLoading, error, refetch } = useHealth()

  return (
    <Card>
      <CardHeader id="system-status" title="Estado del sistema" />
      <StateView
        isLoading={isLoading}
        error={error}
        onRetry={() => void refetch()}
        loadingRows={2}
      >
        {data ? (
          <div className="flex items-center justify-between px-4 py-3">
            <div>
              <p className="text-sm text-neutral-400">Worker</p>
              <Badge tone={data.worker === 'running' ? 'green' : data.worker === 'stopped' ? 'neutral' : 'amber'}>
                {data.worker}
              </Badge>
            </div>
            <div className="text-right">
              <p className="text-sm text-neutral-400">Schema DB</p>
              <p className="text-sm font-medium tabular-nums text-neutral-100">{data.schema_version}</p>
            </div>
          </div>
        ) : null}
      </StateView>
    </Card>
  )
}