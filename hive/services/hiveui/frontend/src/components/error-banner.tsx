export function ErrorBanner({ msg, error }: { msg?: string; error?: Error }) {
  let errorText: string | undefined
  if (error) {
    errorText = error.name !== "Error" ? error.name : error.message
  }

  return (
    <div className="mb-6 max-w-4xl rounded-md border border-destructive bg-destructive/10 p-4">
      {msg && <h2 className="text-destructive text-sm">{msg}</h2>}
      {errorText && <p className="text-destructive text-sm">{errorText}</p>}
    </div>
  )
}
