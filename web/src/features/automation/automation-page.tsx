import { useState } from 'react'
import { Pencil, Plus, Power, Trash2, TriangleAlert } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { EmptyState } from '@/components/ui/state'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { describeRule } from './lib'
import { useAutomationRules } from './use-automation-rules'
import { RuleFormDialog } from './components/rule-form-dialog'
import { toRuleDraft, type AutomationRule, type RuleFormValues } from './types'

/**
 * FASE 5 — Constructor CUÁNDO / ENTONCES.
 * El backend NO tiene /api/automations (🧭 BACKLOG, API_CONTRACT.md §3.10):
 * las reglas se guardan como borradores locales y NUNCA se ejecutan ni se simulan.
 */
export function AutomationPage() {
  const { rules, create, update, remove, toggleEnabled } = useAutomationRules()
  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<AutomationRule | null>(null)
  const [deleting, setDeleting] = useState<AutomationRule | null>(null)

  const handleSubmit = (values: RuleFormValues) => {
    const draft = toRuleDraft(values)
    if (editing) update(editing.id, draft)
    else create(draft)
    setFormOpen(false)
    setEditing(null)
  }

  return (
    <div className="flex flex-col gap-4">
      <div
        role="status"
        className="flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm text-amber-400"
      >
        <TriangleAlert aria-hidden="true" className="mt-0.5 h-4 w-4 shrink-0" />
        <p>
          El backend aún no tiene <code className="rounded bg-black/20 px-1">/api/automations</code> (🧭 BACKLOG).
          Guardás reglas como <strong>borradores locales</strong>; la ejecución se habilitará cuando el backend la respalde.
        </p>
      </div>

      <div className="flex items-center justify-between">
        <p className="text-sm text-neutral-400">
          {rules.length > 0
            ? `${rules.length} ${rules.length === 1 ? 'borrador guardado' : 'borradores guardados'} localmente`
            : 'Definí reglas de automatización; se guardan en este navegador.'}
        </p>
        <Button
          onClick={() => {
            setEditing(null)
            setFormOpen(true)
          }}
        >
          <Plus aria-hidden="true" className="h-4 w-4" />
          Nueva regla
        </Button>
      </div>

      {rules.length === 0 ? (
        <Card>
          <EmptyState
            title="Sin reglas de automatización"
            body="Creá un borrador CUÁNDO → ENTONCES usando solo eventos y acciones que el pipeline ya produce."
            action={
              <Button
                size="sm"
                onClick={() => {
                  setEditing(null)
                  setFormOpen(true)
                }}
              >
                <Plus aria-hidden="true" className="h-4 w-4" />
                Nueva regla
              </Button>
            }
          />
        </Card>
      ) : (
        <ul className="flex flex-col gap-3">
          {rules.map((rule) => (
            <li key={rule.id}>
              <Card className="flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <p className="font-medium text-neutral-100">{rule.name}</p>
                    <Badge tone={rule.enabled ? 'green' : 'neutral'}>{rule.enabled ? 'borrador activo' : 'borrador pausado'}</Badge>
                    <Badge tone="amber">pendiente de backend</Badge>
                  </div>
                  <p className="mt-1 text-sm text-neutral-400" title={describeRule(rule)}>
                    {describeRule(rule)}
                  </p>
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  <Button
                    variant="ghost"
                    size="sm"
                    role="switch"
                    aria-checked={rule.enabled}
                    aria-label={rule.enabled ? `Pausar borrador ${rule.name}` : `Activar borrador ${rule.name}`}
                    title={rule.enabled ? 'Pausar borrador' : 'Activar borrador'}
                    onClick={() => toggleEnabled(rule.id)}
                  >
                    <Power aria-hidden="true" className="h-4 w-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    aria-label={`Editar borrador ${rule.name}`}
                    title="Editar"
                    onClick={() => {
                      setEditing(rule)
                      setFormOpen(true)
                    }}
                  >
                    <Pencil aria-hidden="true" className="h-4 w-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    aria-label={`Eliminar borrador ${rule.name}`}
                    title="Eliminar"
                    onClick={() => setDeleting(rule)}
                  >
                    <Trash2 aria-hidden="true" className="h-4 w-4 text-red-400" />
                  </Button>
                </div>
              </Card>
            </li>
          ))}
        </ul>
      )}

      <RuleFormDialog
        open={formOpen}
        onClose={() => {
          setFormOpen(false)
          setEditing(null)
        }}
        initial={editing}
        onSubmit={handleSubmit}
      />

      <ConfirmDialog
        open={deleting != null}
        onClose={() => setDeleting(null)}
        onConfirm={() => {
          if (deleting) {
            remove(deleting.id)
            setDeleting(null)
          }
        }}
        title="Eliminar borrador"
        message={`¿Eliminar "${deleting?.name}"? El borrador se borra de este navegador; no hay nada ejecutándose.`}
        confirmLabel="Eliminar"
      />
    </div>
  )
}