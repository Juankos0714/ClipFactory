import { Hammer } from 'lucide-react'
import { Card } from '@/components/ui/card'

/** Página honesta para rutas de FASEs posteriores (nunca simular features). */
export function ComingSoon({ name, phase }: { name: string; phase: string }) {
  return (
    <Card className="mx-auto max-w-md">
      <div className="flex flex-col items-center gap-2 px-6 py-12 text-center">
        <Hammer aria-hidden="true" className="h-8 w-8 text-neutral-500" />
        <h2 className="text-base font-semibold text-neutral-100">{name}</h2>
        <p className="text-sm text-neutral-500">
          Previsto para la {phase}. La UI estará acá cuando el backend la respalde.
        </p>
      </div>
    </Card>
  )
}