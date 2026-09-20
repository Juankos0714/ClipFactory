import { useEffect, useState } from 'react'
import { AUTOMATION_STORAGE_KEY, readDraftRules, writeDraftRules } from './storage'
import type { AutomationRule, RuleDraft } from './types'

function newId(): string {
  return crypto.randomUUID()
}

/**
 * CRUD de reglas-borrador sobre localStorage.
 * NO ejecuta nada: solo persiste la configuración local (backend 🧭 BACKLOG).
 */
export function useAutomationRules() {
  const [rules, setRules] = useState<AutomationRule[]>(() =>
    readDraftRules(window.localStorage.getItem(AUTOMATION_STORAGE_KEY)),
  )

  useEffect(() => {
    try {
      window.localStorage.setItem(AUTOMATION_STORAGE_KEY, writeDraftRules(rules))
    } catch {
      // Modo incógnito/privado o cuota llena: la sesión sigue funcionando en memoria.
    }
  }, [rules])

  const create = (draft: RuleDraft) => {
    const now = new Date().toISOString()
    setRules((prev) => [{ id: newId(), enabled: true, ...draft, createdAt: now, updatedAt: now }, ...prev])
  }

  const update = (id: string, draft: RuleDraft) => {
    const now = new Date().toISOString()
    setRules((prev) => prev.map((rule) => (rule.id === id ? { ...rule, ...draft, updatedAt: now } : rule)))
  }

  const remove = (id: string) => {
    setRules((prev) => prev.filter((rule) => rule.id !== id))
  }

  const toggleEnabled = (id: string) => {
    setRules((prev) =>
      prev.map((rule) =>
        rule.id === id ? { ...rule, enabled: !rule.enabled, updatedAt: new Date().toISOString() } : rule,
      ),
    )
  }

  return { rules, create, update, remove, toggleEnabled }
}