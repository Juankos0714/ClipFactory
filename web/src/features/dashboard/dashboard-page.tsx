import { MetricsOverview } from './components/metrics-overview'
import { ProductionQueue } from './components/production-queue'
import { SystemStatus } from './components/system-status'
import { TopClips } from './components/top-clips'

/** Dashboard: estado del sistema + KPIs reales + clips recientes + cola. */
export function DashboardPage() {
  return (
    <div className="flex flex-col gap-4">
      <MetricsOverview />
      <div className="grid gap-4 lg:grid-cols-2">
        <SystemStatus />
        <ProductionQueue />
      </div>
      <TopClips />
    </div>
  )
}