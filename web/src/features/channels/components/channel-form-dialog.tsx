import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { zodResolver } from '@hookform/resolvers/zod'
import { z } from 'zod'
import { Dialog } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Button } from '@/components/ui/button'
import type { Source } from '@/types/api'

const channelSchema = z.object({
  platform: z.enum(['twitch', 'kick'], { errorMap: () => ({ message: 'Elegí una plataforma' }) }),
  channel_id: z.string().trim().min(1, 'El ID/URL del canal es obligatorio'),
  channel_name: z.string().trim().min(1, 'El nombre del canal es obligatorio'),
  active: z.boolean(),
})

export type ChannelFormValues = z.infer<typeof channelSchema>

export function ChannelFormDialog({
  open,
  onClose,
  initial,
  submitting,
  onSubmit,
}: {
  open: boolean
  onClose: () => void
  initial?: Source | null
  submitting: boolean
  onSubmit: (values: ChannelFormValues) => Promise<void>
}) {
  const {
    register,
    handleSubmit,
    reset,
    formState: { errors },
  } = useForm<ChannelFormValues>({
    resolver: zodResolver(channelSchema),
    defaultValues: {
      platform: 'twitch',
      channel_id: '',
      channel_name: '',
      active: true,
    },
  })

  useEffect(() => {
    if (!open) return
    reset(
      initial
        ? { platform: initial.platform, channel_id: initial.channel_id, channel_name: initial.channel_name, active: initial.active }
        : { platform: 'twitch', channel_id: '', channel_name: '', active: true },
    )
  }, [open, initial, reset])

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={initial ? `Editar canal ${initial.channel_name}` : 'Agregar canal'}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={submitting}>
            Cancelar
          </Button>
          <Button form="channel-form" type="submit" loading={submitting} disabled={submitting}>
            {initial ? 'Guardar cambios' : 'Agregar'}
          </Button>
        </>
      }
    >
      <form
        id="channel-form"
        className="flex flex-col gap-4"
        onSubmit={(e) => void handleSubmit((v) => void onSubmit(v))(e)}
        noValidate
      >
        <Select
          label="Plataforma"
          placeholder="Elegí plataforma"
          options={[
            { value: 'twitch', label: 'Twitch' },
            { value: 'kick', label: 'Kick' },
          ]}
          {...register('platform')}
        />
        {errors.platform ? <p role="alert" className="-mt-3 text-xs text-red-400">{errors.platform.message}</p> : null}
        <Input
          label="Channel ID / URL"
          placeholder="4919 o https://twitch.tv/illojuan"
          error={errors.channel_id?.message}
          {...register('channel_id')}
        />
        <Input
          label="Nombre"
          placeholder="illojuan"
          error={errors.channel_name?.message}
          {...register('channel_name')}
        />
        <label className="flex items-center gap-2 text-sm text-neutral-300">
          <input type="checkbox" className="h-4 w-4 accent-brand" {...register('active')} />
          Activo (el discovery lo procesa)
        </label>
      </form>
    </Dialog>
  )
}