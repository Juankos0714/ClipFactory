/**
 * Contrato LOCAL del borrador de automatización (API_CONTRACT.md §3.10).
 * NO son DTOs del contrato REST: el backend no tiene /api/automations (🧭 BACKLOG).
 * Los eventos (trigger) y acciones (action) usan SOLO lo que el pipeline real produce.
 */
import { z } from 'zod'
import type { SelectOption } from '@/components/ui/select'

/** Eventos que el pipeline ya emite (statuses de source_clips/jobs/publications). */
export const TRIGGERS = ['clip_detected', 'download_completed', 'clip_processed', 'publication_failed'] as const
export type AutomationTrigger = (typeof TRIGGERS)[number]

/** Acciones que el backend YA sabe encolar hoy (jobs download/process/publish, skip). */
export const ACTIONS = ['download', 'process', 'publish_youtube', 'publish_meta', 'skip'] as const
export type AutomationAction = (typeof ACTIONS)[number]

/** Condición de plataforma; '' = cualquier plataforma. */
export const CONDITION_PLATFORMS = ['twitch', 'kick', ''] as const
export type ConditionPlatform = (typeof CONDITION_PLATFORMS)[number]

export const TRIGGER_LABELS: Record<AutomationTrigger, string> = {
  clip_detected: 'Cuando se detecta un clip',
  download_completed: 'Cuando la descarga finaliza',
  clip_processed: 'Cuando el clip se procesa',
  publication_failed: 'Cuando una publicación falla',
}

export const ACTION_LABELS: Record<AutomationAction, string> = {
  download: 'Descargar el clip',
  process: 'Procesar (recortar) el clip',
  publish_youtube: 'Publicar en YouTube',
  publish_meta: 'Publicar en Meta',
  skip: 'Descartar (skip)',
}

export const PLATFORM_LABELS: Record<Exclude<ConditionPlatform, ''>, string> = {
  twitch: 'Twitch',
  kick: 'Kick',
}

export const TRIGGER_OPTIONS: SelectOption[] = TRIGGERS.map((t) => ({
  value: t,
  label: TRIGGER_LABELS[t],
}))

export const ACTION_OPTIONS: SelectOption[] = ACTIONS.map((a) => ({
  value: a,
  label: ACTION_LABELS[a],
}))

export interface AutomationConditions {
  platform: ConditionPlatform
  source_id: number | null
  min_duration_seconds: number | null
}

/** Borrador de regla guardado en localStorage (pendiente de backend). */
export interface AutomationRule {
  id: string
  name: string
  enabled: boolean
  trigger: AutomationTrigger
  conditions: AutomationConditions
  action: AutomationAction
  createdAt: string
  updatedAt: string
}

/** Input de creación/edición (id y timestamps los genera el hook). */
export interface RuleDraft {
  name: string
  trigger: AutomationTrigger
  conditions: AutomationConditions
  action: AutomationAction
}

/** Esquema de persistencia: si el JSON guardado no cumple esto, se descarta. */
export const automationRuleSchema = z.object({
  id: z.string().min(1),
  name: z.string().min(1),
  enabled: z.boolean(),
  trigger: z.enum(TRIGGERS),
  conditions: z.object({
    platform: z.enum(CONDITION_PLATFORMS),
    source_id: z.number().nullable(),
    min_duration_seconds: z.number().nullable(),
  }),
  action: z.enum(ACTIONS),
  createdAt: z.string(),
  updatedAt: z.string(),
})

const optionalPositiveNumber = z.preprocess(
  (value) => (typeof value === 'number' && Number.isNaN(value) ? undefined : value),
  z.number().positive('Debe ser mayor a 0').optional(),
)

/** Esquema del formulario CUÁNDO → ENTONCES (RHF + Zod). */
export const ruleFormSchema = z.object({
  name: z.string().trim().min(1, 'El nombre es obligatorio'),
  trigger: z.enum(TRIGGERS, { errorMap: () => ({ message: 'Elegí un evento' }) }),
  platform: z.enum(CONDITION_PLATFORMS, { errorMap: () => ({ message: 'Elegí una opción' }) }),
  source_id: optionalPositiveNumber,
  min_duration_seconds: optionalPositiveNumber,
  action: z.enum(ACTIONS, { errorMap: () => ({ message: 'Elegí una acción' }) }),
})

export type RuleFormValues = z.infer<typeof ruleFormSchema>

/** Formulario → borrador persistible ('' y undefined → sin condición). */
export function toRuleDraft(values: RuleFormValues): RuleDraft {
  return {
    name: values.name,
    trigger: values.trigger,
    conditions: {
      platform: values.platform,
      source_id: values.source_id ?? null,
      min_duration_seconds: values.min_duration_seconds ?? null,
    },
    action: values.action,
  }
}