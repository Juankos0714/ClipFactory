import { ChevronLeft, ChevronRight } from 'lucide-react'
import { Button } from './button'
import type { PaginationMeta } from '@/types/api'

export function Pagination({
  meta,
  onChange,
}: {
  meta: PaginationMeta
  onChange: (page: number) => void
}) {
  if (meta.pageCount <= 1 && !meta.hasPrev && !meta.hasNext) return null
  return (
    <nav aria-label="Paginación" className="flex items-center justify-between gap-3 border-t border-border px-4 py-3">
      <p className="text-xs text-neutral-500">
        Página {meta.page} de {meta.pageCount} · {meta.total} en total
      </p>
      <div className="flex items-center gap-2">
        <Button
          variant="secondary"
          size="sm"
          disabled={!meta.hasPrev}
          onClick={() => onChange(meta.page - 1)}
          aria-label="Página anterior"
        >
          <ChevronLeft aria-hidden="true" className="h-4 w-4" />
        </Button>
        <Button
          variant="secondary"
          size="sm"
          disabled={!meta.hasNext}
          onClick={() => onChange(meta.page + 1)}
          aria-label="Página siguiente"
        >
          <ChevronRight aria-hidden="true" className="h-4 w-4" />
        </Button>
      </div>
    </nav>
  )
}