import { Select } from '@/components/ui/select'
import { CLIP_STATUS_OPTIONS } from '@/features/clips/constants'
import type { ClipStatusFilter, SourcePlatform } from '@/types/api'

export interface ProductionFiltersState {
  platform?: SourcePlatform
  status?: ClipStatusFilter
}

export function ProductionFilters({
  value,
  onChange,
  onReset,
}: {
  value: ProductionFiltersState
  onChange: (patch: Partial<ProductionFiltersState>) => void
  onReset: () => void
}) {
  return (
    <form
      className="grid grid-cols-2 gap-3 md:grid-cols-3"
      onSubmit={(e) => e.preventDefault()}
      aria-label="Filtros del pipeline"
    >
      <Select
        label="Plataforma"
        name="platform"
        placeholder="Todas"
        options={[
          { value: 'twitch', label: 'Twitch' },
          { value: 'kick', label: 'Kick' },
        ]}
        value={value.platform ?? ''}
        onChange={(e) => onChange({ platform: (e.target.value || undefined) as SourcePlatform | undefined })}
      />
      <Select
        label="Estado"
        name="status"
        placeholder="Todos"
        options={CLIP_STATUS_OPTIONS}
        value={value.status ?? ''}
        onChange={(e) => onChange({ status: (e.target.value || undefined) as ClipStatusFilter | undefined })}
      />
      <div className="flex items-end">
        <button
          type="button"
          onClick={onReset}
          className="rounded-md px-2 py-1 text-xs font-medium text-neutral-400 hover:text-neutral-100"
        >
          Limpiar filtros
        </button>
      </div>
    </form>
  )
}