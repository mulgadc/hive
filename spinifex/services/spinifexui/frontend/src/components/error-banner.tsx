export function ErrorBanner({ msg, error }: { msg?: string; error?: Error }) {
  let errorText: string | undefined
  if (error) {
    errorText = error.name === "Error" ? error.message : error.name
  }

  return (
    <div className="mb-6 max-w-4xl rounded-md border border-destructive bg-destructive/10 p-4">
      {msg && <h2 className="text-sm text-destructive">{msg}</h2>}
      {errorText && <p className="text-sm text-destructive">{errorText}</p>}
    </div>
  )
}
