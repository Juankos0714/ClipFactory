import { z } from 'zod'
import { automationRuleSchema, type AutomationRule } from './types'

/** Clave versionada: si el formato cambia, se cambia el sufijo y lo viejo se descarta. */
export const AUTOMATION_STORAGE_KEY = 'clipfactory.automation.drafts.v1'

/**
 * Lee y valida el JSON guardado. JSON roto, de otra forma o con entradas
 * inválidas → lista vacía (nunca rompe el arranque de la UI).
 */
export function readDraftRules(raw: string | null): AutomationRule[] {
  if (!raw) return []
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return []
  }
  if (!Array.isArray(parsed)) return []
  const validated = z.array(automationRuleSchema).safeParse(parsed)
  return validated.success ? validated.data : []
}

/** Serializa los borradores para localStorage. */
export function writeDraftRules(rules: AutomationRule[]): string {
  return JSON.stringify(rules)
}