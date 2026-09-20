import { TriangleAlert } from 'lucide-react'
import { usePublicationsList } from '@/hooks/use-publications'
import { useSystemOverview } from '@/hooks/use-system'
import { Card, CardHeader } from '@/components/ui/card'
import { EmptyState, StateView } from '@/components/ui/state'
import { PublishedArea, StackedBars } from './charts'
import {
  JOB_STATUS_COLORS,
  JOB_STATUSES,
  PUB_STATUS_COLORS,
  PUBLICATION_STATUSES,
  jobsStack,
  publishedPerDay,
  publicationStack,
} from './lib'

const PUB_STATUS_LABELS: Record<string, string> = {
  pending: 'Pendiente',
  published: 'Publicada',
  error: 'Error',
  waiting_rate_limit: 'Rate limit',
  failed: 'Fallida',
}

const JOB_STATUS_LABELS: Record<string, string> = {
  queued: 'En cola',
  running: 'Ejecutando',
  done: 'Finalizado',
  error: 'Error',
}

const JOB_TYPE_LABELS: Record<string, string> = {
  discovery: 'Discovery',
  download: 'Descarga',
  process: 'Procesado',
  thumbnail: 'Miniatura',
  publish: 'Publicación',
  poll_publications: 'Poll pubs',
}

const PUB_PLATFORM_LABELS: Record<string, string> = {
  youtube: 'YouTube',
  meta: 'Meta',
}

const DAYS = 30

/**
 * Analytics derivada de la DB (API_CONTRACT.md §3.9 🧭 BACKLOG).
 * El backend NO recolecta views/engagement: aquí se grafica lo que la DB ya
 * tiene (estados, fechas, intentos). Nada se simula ni se inventa.
 */
export function AnalyticsPage() {
  const overview = useSystemOverview()
  const pubs = usePublicationsList({ pageSize: 100 })

  const isLoading = overview.isLoading || pubs.isLoading
  const error = overview.error ?? pubs.error

  const data = overview.data
  const publicationRows = data ? publicationStack(data) : []
  const jobRows = data ? jobsStack(data) : []
  const daily = pubs.data ? publishedPerDay(pubs.data.data, DAYS) : []
  const totalPublished = daily.reduce((sum, d) => sum + d.published, 0)

  return (
    <div className="flex flex-col gap-4">
      <div
        role="status"
        className="flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm text-amber-400"
      >
        <TriangleAlert aria-hidden="true" className="mt-0.5 h-4 w-4 shrink-0" />
        <p>
          Las métricas de plataforma (views, engagement) son <strong>🧭 BACKLOG</strong>: el backend aún no las
          recolecta. Estos gráficos usan datos que la DB ya tiene — estados, fechas e intentos — sin simular nada.
        </p>
      </div>

      <StateView isLoading={isLoading} error={error} loadingRows={4}>
        <div className="grid gap-4 xl:grid-cols-2">
          <Card>
            <CardHeader id="publications-chart" title="Publicaciones por plataforma y estado" />
            <div className="p-4">
              {publicationRows.length === 0 ? (
                <EmptyState title="Sin publicaciones todavía" body="Cuando haya intentos de publicación, aparecerán acá." />
              ) : (
                <>
                  <StackedBars
                    data={publicationRows.map((row) => ({ ...row, platform: PUB_PLATFORM_LABELS[row.platform] ?? row.platform }))}
                    xKey="platform"
                    keys={PUBLICATION_STATUSES}
                    labels={PUB_STATUS_LABELS}
                    colors={PUB_STATUS_COLORS}
                    ariaLabel="Publicaciones por plataforma y estado"
                  />
                  <p className="mt-2 text-xs text-neutral-500">
                    {publicationRows
                      .map((row) => `${row.platform}: ${PUBLICATION_STATUSES.reduce((s, k) => s + (Number(row[k]) || 0), 0)}`)
                      .join(' · ')}
                  </p>
                </>
              )}
            </div>
          </Card>

          <Card>
            <CardHeader id="jobs-chart" title="Jobs por tipo y estado" />
            <div className="p-4">
              {jobRows.length === 0 ? (
                <EmptyState title="Sin jobs todavía" body="La cola de trabajo aparece acá cuando el worker encole algo." />
              ) : (
                <>
                  <StackedBars
                    data={jobRows.map((row) => ({ ...row, type: JOB_TYPE_LABELS[row.type] ?? row.type }))}
                    xKey="type"
                    keys={JOB_STATUSES}
                    labels={JOB_STATUS_LABELS}
                    colors={JOB_STATUS_COLORS}
                    ariaLabel="Jobs por tipo y estado"
                  />
                  <p className="mt-2 text-xs text-neutral-500">
                    {jobRows
                      .map((row) => `${JOB_TYPE_LABELS[row.type] ?? row.type}: ${JOB_STATUSES.reduce((s, k) => s + (Number(row[k]) || 0), 0)}`)
                      .join(' · ')}
                  </p>
                </>
              )}
            </div>
          </Card>

          <Card className="xl:col-span-2">
            <CardHeader id="published-trend" title={`Publicadas por día (últimos ${DAYS} días)`} />
            <div className="p-4">
              {totalPublished === 0 ? (
                <EmptyState
                  title="Aún no hay publicaciones con fecha"
                  body="Las fechas provienen de publications.published_at; el poll de reconciliación las actualiza."
                />
              ) : (
                <>
                  <PublishedArea data={daily} ariaLabel="Publicaciones publicadas por día" />
                  <p className="mt-2 text-xs text-neutral-500">
                    {totalPublished} publicadas en los últimos {DAYS} días (sobre las últimas 100 publicaciones).
                  </p>
                </>
              )}
            </div>
          </Card>
        </div>
      </StateView>
    </div>
  )
}