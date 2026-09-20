import { useEffect } from 'react'
import { useForm, type Resolver } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { Dialog } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Button } from '@/components/ui/button'
import {
  ACTION_OPTIONS,
  TRIGGER_OPTIONS,
  ruleFormSchema,
  type AutomationRule,
  type RuleFormValues,
} from '../types'

const EMPTY_VALUES: RuleFormValues = {
  name: '',
  trigger: 'clip_detected',
  platform: '',
  source_id: undefined,
  min_duration_seconds: undefined,
  action: 'download',
}

function toFormValues(rule: AutomationRule): RuleFormValues {
  return {
    name: rule.name,
    trigger: rule.trigger,
    platform: rule.conditions.platform,
    source_id: rule.conditions.source_id ?? undefined,
    min_duration_seconds: rule.conditions.min_duration_seconds ?? undefined,
    action: rule.action,
  }
}

export function RuleFormDialog({
  open,
  onClose,
  initial,
  onSubmit,
}: {
  open: boolean
  onClose: () => void
  initial?: AutomationRule | null
  onSubmit: (values: RuleFormValues) => void
}) {
  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<RuleFormValues>({
    resolver: zodResolver(ruleFormSchema) as Resolver<RuleFormValues>,
    defaultValues: EMPTY_VALUES,
  })

  useEffect(() => {
    if (!open) return
    reset(initial ? toFormValues(initial) : EMPTY_VALUES)
  }, [open, initial, reset])

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={initial ? 'Editar regla' : 'Nueva regla de automatización'}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            Cancelar
          </Button>
          <Button form="rule-form" type="submit">
            {initial ? 'Guardar cambios' : 'Crear regla'}
          </Button>
        </>
      }
    >
      <form
        id="rule-form"
        className="flex flex-col gap-4"
        onSubmit={(e) => void handleSubmit(onSubmit)(e)}
        noValidate
      >
        <Input
          label="Nombre"
          placeholder="Ej.: Descargar clips largos de illojuan"
          error={errors.name?.message}
          {...register('name')}
        />

        <div className="flex flex-col gap-1">
          <Select label="CUÁNDO (evento del pipeline)" options={TRIGGER_OPTIONS} {...register('trigger')} />
          {errors.trigger ? (
            <p role="alert" className="text-xs text-red-400">
              {errors.trigger.message}
            </p>
          ) : (
            <p className="text-xs text-neutral-500">Eventos reales que el pipeline ya produce.</p>
          )}
        </div>

        <fieldset className="flex flex-col gap-3 rounded-md border border-border p-3">
          <legend className="px-1 text-sm font-medium text-neutral-300">Condiciones (opcionales)</legend>
          <Select
            label="Plataforma"
            placeholder="Cualquiera"
            options={[
              { value: 'twitch', label: 'Twitch' },
              { value: 'kick', label: 'Kick' },
            ]}
            {...register('platform')}
          />
          <div className="grid gap-3 sm:grid-cols-2">
            <Input
              label="ID del canal (source_id)"
              type="number"
              min={1}
              placeholder="Cualquiera"
              hint="Opcional"
              error={errors.source_id?.message}
              {...register('source_id', { valueAsNumber: true })}
            />
            <Input
              label="Duración mínima (segundos)"
              type="number"
              min={1}
              placeholder="Sin mínimo"
              hint="Opcional"
              error={errors.min_duration_seconds?.message}
              {...register('min_duration_seconds', { valueAsNumber: true })}
            />
          </div>
        </fieldset>

        <div className="flex flex-col gap-1">
          <Select label="ENTONCES (acción)" options={ACTION_OPTIONS} {...register('action')} />
          {errors.action ? (
            <p role="alert" className="text-xs text-red-400">
              {errors.action.message}
            </p>
          ) : (
            <p className="text-xs text-neutral-500">Acciones que el backend ya sabe encolar.</p>
          )}
        </div>
      </form>
    </Dialog>
  )
}