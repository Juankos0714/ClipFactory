import { Link } from 'react-router-dom'
import { Card } from '@/components/ui/card'
import { Button } from '@/components/ui/button'

export function NotFound() {
  return (
    <Card className="mx-auto max-w-md">
      <div className="flex flex-col items-center gap-3 px-6 py-12 text-center">
        <p className="text-3xl font-semibold text-neutral-200">404</p>
        <p className="text-sm text-neutral-500">La página que buscás no existe.</p>
        <Link to="/">
          <Button variant="secondary" size="sm">
            Volver al dashboard
          </Button>
        </Link>
      </div>
    </Card>
  )
}