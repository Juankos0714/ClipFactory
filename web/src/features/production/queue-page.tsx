import { useState } from 'react'
import { RefreshCw, XCircle } from 'lucide-react'
import { useCancelJob, useJobsList, useJobsStats, useRetryJob } from '@/hooks/use-jobs'
import { REFRESH_DASHBOARD_MS } from '@/lib/config/env'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardHeader } from '@/components/ui/card'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { MetricCard } from '@/components/shared/metric-card'
import { Pagination } from '@/components/ui/pagination'
import { Select, type SelectOption } from '@/components/ui/select'
import { StateView } from '@/components/ui/state'
import { formatDateTime } from '@/lib/format'
import type { Job, JobStatus, JobType } from '@/types/api'

const PAGE_SIZE = 20

const JOB_TYPE_LABELS: Record<JobType, string> = {
  discovery: 'Descubrimiento',
  download: 'Descarga',
  process: 'Procesado',
  thumbnail: 'Miniatura',
  publish: 'Publicación',
  poll_publications: 'Poll publicaciones',
}

const JOB_TYPE_OPTIONS: SelectOption[] = Object.entries(JOB_TYPE_LABELS).map(([value, label]) => ({ value, label }))

const JOB_STATUS_OPTIONS: SelectOption[] = [
  { value: 'queued', label: 'En cola' },
  { value: 'running', label: 'Ejecutando' },
  { value: 'error', label: 'Error' },
  { value: 'done', label: 'Finalizado' },
]

/** Conteos por estado de GET /api/jobs/stats (totales entre todos los tipos). */
function JobStatsSummary() {
  const { data, isLoading, error } = useJobsStats()
  const totalBy = (s: JobStatus): number =>
    Object.values(data ?? {}).reduce((sum, byType) => sum + (byType[s] ?? 0), 0)

  return (
    <Card>
      <CardHeader id="job-stats" title="Resumen de la cola" />
      <StateView isLoading={isLoading} error={error} loadingRows={1}>
        {data ? (
          <div className="grid grid-cols-2 gap-3 p-4 md:grid-cols-4">
            <MetricCard label="En cola" value={totalBy('queued')} accent="blue" />
            <MetricCard label="Ejecutando" value={totalBy('running')} accent="amber" />
            <MetricCard label="Errores" value={totalBy('error')} accent="red" />
            <MetricCard label="Finalizados" value={totalBy('done')} accent="green" />
          </div>
        ) : null}
      </StateView>
    </Card>
  )
}

/** Cola de jobs: filtros + tabla paginada + retry/cancel + polling. */
export function QueuePage() {
  const [page, setPage] = useState(1)
  const [type, setType] = useState<JobType | ''>('')
  const [status, setStatus] = useState<JobStatus | ''>('')
  const [includeDone, setIncludeDone] = useState(false)
  const [retryTarget, setRetryTarget] = useState<Job | null>(null)
  const [cancelTarget, setCancelTarget] = useState<Job | null>(null)

  const retryJob = useRetryJob()
  const cancelJob = useCancelJob()

  const { data, isLoading, error, refetch } = useJobsList({
    page,
    pageSize: PAGE_SIZE,
    type: type || undefined,
    status: includeDone ? 'done' : status || undefined,
  })

  const jobs = data?.data ?? []

  const handleType = (v: string) => {
    setType(v as JobType | '')
    setPage(1)
  }
  const handleStatus = (v: string) => {
    setStatus(v as JobStatus | '')
    if (v) setIncludeDone(false)
    setPage(1)
  }
  const handleIncludeDone = (on: boolean) => {
    setIncludeDone(on)
    if (on) setStatus('')
    setPage(1)
  }

  const confirmRetry = async () => {
    if (!retryTarget) return
    try {
      await retryJob.mutateAsync(retryTarget.id)
      setRetryTarget(null)
    } catch {
      // El diálogo se queda abierto mostrando el error.
    }
  }
  const confirmCancel = async () => {
    if (!cancelTarget) return
    try {
      await cancelJob.mutateAsync(cancelTarget.id)
      setCancelTarget(null)
    } catch {
      // El diálogo se queda abierto mostrando el error.
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <JobStatsSummary />

      <Card className="p-4">
        <form
          className="grid grid-cols-1 gap-3 md:grid-cols-3"
          onSubmit={(e) => e.preventDefault()}
          aria-label="Filtros de la cola"
        >
          <Select
            label="Tipo de job"
            name="job-type"
            placeholder="Todos"
            options={JOB_TYPE_OPTIONS}
            value={type}
            onChange={(e) => handleType(e.target.value)}
          />
          <Select
            label="Estado"
            name="job-status"
            placeholder="Todos"
            options={JOB_STATUS_OPTIONS}
            value={status}
            disabled={includeDone}
            onChange={(e) => handleStatus(e.target.value)}
          />
          <label className="flex items-end gap-2 pb-2 text-sm text-neutral-300">
            <input
              type="checkbox"
              checked={includeDone}
              onChange={(e) => handleIncludeDone(e.target.checked)}
              className="h-4 w-4 accent-brand"
            />
            Incluir finalizados (done)
          </label>
        </form>
        <p className="mt-2 text-xs text-neutral-500">
          El listado por defecto oculta los jobs finalizados; activá el toggle para verlos (filtro{' '}
          <span className="text-neutral-400">status=done</span>). Refresco automático cada {REFRESH_DASHBOARD_MS / 1000}s.
        </p>
      </Card>

      <Card>
        <StateView
          isLoading={isLoading}
          error={error}
          onRetry={() => void refetch()}
          isEmpty={jobs.length === 0}
          emptyTitle="No hay jobs con estos filtros"
          emptyBody="Activá 'Incluir finalizados' para ver el historial, o esperá a que el worker encolé nuevo trabajo."
          loadingRows={8}
        >
          <div className="overflow-x-auto">
            <table className="w-full min-w-[900px] text-left text-sm">
              <thead>
                <tr className="border-b border-border text-xs uppercase tracking-wide text-neutral-500">
                  <th scope="col" className="px-3 py-3 font-medium">ID</th>
                  <th scope="col" className="px-3 py-3 font-medium">Tipo</th>
                  <th scope="col" className="px-3 py-3 font-medium">Referencia</th>
                  <th scope="col" className="px-3 py-3 font-medium">Estado</th>
                  <th scope="col" className="px-3 py-3 font-medium">Intentos</th>
                  <th scope="col" className="px-3 py-3 font-medium">Actualizado</th>
                  <th scope="col" className="px-3 py-3 font-medium">Error</th>
                  <th scope="col" className="px-3 py-3 text-right font-medium">Acciones</th>
                </tr>
              </thead>
              <tbody>
                {jobs.map((job) => (
                  <tr key={job.id} className="border-b border-border/60 last:border-0">
                    <td className="px-3 py-3 tabular-nums text-neutral-400">{job.id}</td>
                    <td className="px-3 py-3 text-neutral-200">{JOB_TYPE_LABELS[job.type]}</td>
                    <td className="px-3 py-3 text-neutral-400">
                      {job.reference_type}:{job.reference_id}
                    </td>
                    <td className="px-3 py-3">
                      <StatusBadge status={job.status} />
                    </td>
                    <td className="px-3 py-3 tabular-nums text-neutral-400">{job.attempts}</td>
                    <td className="px-3 py-3 text-neutral-400">{formatDateTime(job.updated_at)}</td>
                    <td className="max-w-[220px] px-3 py-3 text-xs text-red-400">
                      {job.error_message ? (
                        <span className="truncate" title={job.error_message}>
                          {job.error_message}
                        </span>
                      ) : (
                        <span className="text-neutral-600">—</span>
                      )}
                    </td>
                    <td className="px-3 py-3">
                      <div className="flex flex-wrap items-center justify-end gap-1">
                        {job.status === 'error' ? (
                          <Button
                            size="sm"
                            variant="danger"
                            loading={retryJob.isPending && retryTarget?.id === job.id}
                            onClick={() => setRetryTarget(job)}
                          >
                            <RefreshCw aria-hidden="true" className="h-4 w-4" />
                            Reintentar
                          </Button>
                        ) : null}
                        {job.status === 'queued' || job.status === 'running' ? (
                          <Button
                            size="sm"
                            variant="ghost"
                            loading={cancelJob.isPending && cancelTarget?.id === job.id}
                            onClick={() => setCancelTarget(job)}
                          >
                            <XCircle aria-hidden="true" className="h-4 w-4" />
                            Cancelar
                          </Button>
                        ) : null}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {data ? <Pagination meta={data.pagination} onChange={(p) => setPage(p)} /> : null}
        </StateView>
      </Card>

      <ConfirmDialog
        open={retryTarget != null}
        onClose={() => setRetryTarget(null)}
        onConfirm={() => void confirmRetry()}
        title="Reintentar job"
        message={
          retryTarget
            ? retryJob.error
              ? `${retryJob.error.message} Probá más tarde o revisá los logs.`
              : `Se re-encolará el job #${retryTarget.id} (${JOB_TYPE_LABELS[retryTarget.type]}). El worker lo volverá a ejecutar.`
            : ''
        }
        confirmLabel="Reintentar"
        loading={retryJob.isPending}
      />
      <ConfirmDialog
        open={cancelTarget != null}
        onClose={() => setCancelTarget(null)}
        onConfirm={() => void confirmCancel()}
        title="Cancelar job"
        message={
          cancelTarget
            ? cancelJob.error
              ? `${cancelJob.error.message} El job puede seguir ejecutándose si ya arrancó.`
              : `Se marcará el job #${cancelTarget.id} (${JOB_TYPE_LABELS[cancelTarget.type]}) como error y no se ejecutará.`
            : ''
        }
        confirmLabel="Cancelar"
        loading={cancelJob.isPending}
      />
    </div>
  )
}