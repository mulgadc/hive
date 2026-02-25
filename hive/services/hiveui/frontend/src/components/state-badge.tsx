export function StateBadge({ state }: { state: string | undefined }) {
  let className =
    "bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-100"

  if (state === "running" || state === "available" || state === "completed") {
    className =
      "bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-100"
  } else if (state === "stopped" || state === "error") {
    className = "bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-100"
  } else if (
    state === "pending" ||
    state === "shutting-down" ||
    state === "stopping"
  ) {
    className =
      "bg-yellow-100 text-yellow-800 dark:bg-yellow-900 dark:text-yellow-100"
  } else if (state === "terminated") {
    className = "bg-zinc-100 text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"
  }

  return (
    <div className={`rounded-full px-2 py-1 text-xs ${className}`}>{state}</div>
  )
}
