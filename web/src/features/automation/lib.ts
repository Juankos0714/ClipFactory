import { ACTION_LABELS, PLATFORM_LABELS, TRIGGER_LABELS, type AutomationAction, type AutomationConditions, type AutomationTrigger } from './types'

/** Condiciones como texto legible: 'en Twitch', 'del canal #2', 'de al menos 40 s'. */
export function describeConditions(conditions: AutomationConditions): string[] {
  const parts: string[] = []
  if (conditions.platform) parts.push(`en ${PLATFORM_LABELS[conditions.platform]}`)
  if (conditions.source_id != null) parts.push(`del canal #${conditions.source_id}`)
  if (conditions.min_duration_seconds != null) parts.push(`de al menos ${conditions.min_duration_seconds} s`)
  return parts
}

/** Frase CUÁNDO → ENTONCES del borrador (presentacional; el backend no la ejecuta). */
export function describeRule(rule: {
  trigger: AutomationTrigger
  conditions: AutomationConditions
  action: AutomationAction
}): string {
  const conditions = describeConditions(rule.conditions)
  const when = conditions.length > 0 ? `${TRIGGER_LABELS[rule.trigger]} (${conditions.join(' · ')})` : TRIGGER_LABELS[rule.trigger]
  return `${when} → ${ACTION_LABELS[rule.action]}`
}