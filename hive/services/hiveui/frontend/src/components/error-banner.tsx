export function ErrorBanner({ msg, error }: { msg?: string; error?: Error }) {
  return (
    <div className="mb-6 max-w-4xl rounded-md border border-destructive bg-destructive/10 p-4">
      {msg && <h2 className="text-destructive text-sm">{msg}</h2>}
      {error && <p className="text-destructive text-sm">{error.message}</p>}
    </div>
  )
}
