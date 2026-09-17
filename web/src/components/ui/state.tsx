import type { ReactNode } from 'react'
import { AlertTriangle, Inbox } from 'lucide-react'
import { Button } from './button'
import { Card } from './card'
import { Skeleton } from './skeleton'

export function EmptyState({
  title,
  body,
  action,
}: {
  title: string
  body?: string
  action?: ReactNode
}) {
  return (
    <div className="flex flex-col items-center gap-2 px-6 py-10 text-center">
      <Inbox aria-hidden="true" className="h-8 w-8 text-neutral-500" />
      <p className="text-sm font-medium text-neutral-300">{title}</p>
      {body ? <p className="text-sm text-neutral-500">{body}</p> : null}
      {action}
    </div>
  )
}

export function ErrorState({
  title = 'No se pudo cargar',
  body,
  onRetry,
}: {
  title?: string
  body?: string
  onRetry?: () => void
}) {
  return (
    <div className="flex flex-col items-center gap-2 px-6 py-10 text-center" role="alert">
      <AlertTriangle aria-hidden="true" className="h-8 w-8 text-amber-400" />
      <p className="text-sm font-medium text-neutral-200">{title}</p>
      {body ? <p className="max-w-md text-sm text-neutral-500">{body}</p> : null}
      {onRetry ? (
        <Button variant="secondary" size="sm" onClick={onRetry}>
          Reintentar
        </Button>
      ) : null}
    </div>
  )
}

export function LoadingState({ rows = 5 }: { rows?: number }) {
  return (
    <div className="p-4" role="status" aria-label="Cargando">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="mb-3 h-10 rounded-md bg-border/60" />
      ))}
    </div>
  )
}

/**
 * Estado combinado (5 estados: loading/error/empty/success).
 * - isLoading/error: reemplaza al contenido.
 * - isEmpty: muestra vacío (solo si no está cargando ni en error).
 */
export function StateView({
  isLoading,
  error,
  isEmpty,
  emptyTitle,
  emptyBody,
  emptyAction,
  onRetry,
  errorBody,
  loadingRows,
  children,
}: {
  isLoading: boolean
  error?: unknown
  isEmpty?: boolean
  emptyTitle?: string
  emptyBody?: string
  emptyAction?: ReactNode
  onRetry?: () => void
  errorBody?: string
  loadingRows?: number
  children: ReactNode
}) {
  if (isLoading) return <LoadingState rows={loadingRows} />
  if (error) {
    const message = error instanceof Error ? error.message : undefined
    return <ErrorState body={message ?? errorBody} onRetry={onRetry} />
  }
  if (isEmpty) return <EmptyState title={emptyTitle ?? 'Sin datos'} body={emptyBody} action={emptyAction} />
  return <>{children}</>
}

export function CardState({ title, children }: { title: string; children: ReactNode }) {
  return (
    <Card>
      <div className="p-4">
        <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-neutral-400">{title}</h3>
        {children}
      </div>
    </Card>
  )
}