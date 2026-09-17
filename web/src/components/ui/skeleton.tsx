import type { HTMLAttributes } from 'react'

/** Bloque de placeholder shimmer con forma tipo "card". */
export function Skeleton({ className = 'h-4 w-full rounded-md bg-border', ...rest }: HTMLAttributes<HTMLDivElement>) {
  return <div className={`animate-pulse bg-border/60 ${className}`} {...rest} />
}

/** Esqueleto de una fila de tabla (5 columnas). */
export function TableSkeletonRows({ rows = 5 }: { rows?: number }) {
  return (
    <div className="flex flex-col gap-2 p-4" role="status" aria-label="Cargando filas">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="grid grid-cols-2 gap-4 md:grid-cols-5">
          <Skeleton className="h-4 rounded bg-border/60 md:col-span-2" />
          <Skeleton className="hidden h-4 rounded bg-border/60 md:block" />
          <Skeleton className="hidden h-4 rounded bg-border/60 md:block" />
          <Skeleton className="h-4 rounded bg-border/60" />
        </div>
      ))}
    </div>
  )
}