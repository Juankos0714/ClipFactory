import { Select, type SelectOption } from '@/components/ui/select'
import { CLIP_STATUS_OPTIONS } from '../constants'

export interface ClipFiltersState {
  platform?: string
  status?: string
  channelId?: string
  sort?: string
}

export function ClipFilters({
  value,
  channels,
  onChange,
  onReset,
}: {
  value: ClipFiltersState
  channels: SelectOption[]
  onChange: (patch: Partial<ClipFiltersState>) => void
  onReset: () => void
}) {
  return (
    <form
      className="grid grid-cols-2 gap-3 md:grid-cols-4"
      onSubmit={(e) => e.preventDefault()}
      aria-label="Filtros de clips"
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
        onChange={(e) => onChange({ platform: e.target.value || undefined })}
      />
      <Select
        label="Canal"
        name="channelId"
        placeholder="Todos"
        options={channels}
        value={value.channelId ?? ''}
        onChange={(e) => onChange({ channelId: e.target.value || undefined })}
      />
      <Select
        label="Estado"
        name="status"
        placeholder="Todos"
        options={CLIP_STATUS_OPTIONS}
        value={value.status ?? ''}
        onChange={(e) => onChange({ status: e.target.value || undefined })}
      />
      <Select
        label="Ordenar"
        name="sort"
        options={[
          { value: 'newest', label: 'Más recientes' },
          { value: 'duration', label: 'Duración' },
        ]}
        value={value.sort ?? 'newest'}
        onChange={(e) => onChange({ sort: e.target.value || undefined })}
      />
      <p className="col-span-2 text-xs text-neutral-500 md:col-span-4">
        Ordenar por <span className="text-neutral-400">views/engagement</span> requiere el backend de métricas
        (BACKLOG del contrato) — no disponible hasta entonces.
      </p>
      <button
        type="button"
        onClick={onReset}
        className="col-span-2 justify-self-start rounded-md px-2 py-1 text-xs font-medium text-neutral-400 hover:text-neutral-100 md:col-span-4"
      >
        Limpiar filtros
      </button>
    </form>
  )
}