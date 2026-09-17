import type { SelectOption } from '@/components/ui/select'

export const CLIP_STATUS_OPTIONS: SelectOption[] = [
  { value: 'detected', label: 'Detectado' },
  { value: 'downloaded', label: 'Descargado' },
  { value: 'processing', label: 'Procesando' },
  { value: 'completed', label: 'Completado' },
  { value: 'skipped', label: 'Omitido' },
  { value: 'failed', label: 'Fallido' },
  { value: 'error', label: 'Error' },
]