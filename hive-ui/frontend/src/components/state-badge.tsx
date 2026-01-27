export function StateBadge({ state }: { state: string | undefined }) {
  let className =
    "bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-100"

  if (state === "running") {
    className =
      "bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-100"
  } else if (state === "stopped") {
    className = "bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-100"
  }

  return (
    <div className={`rounded-full px-2 py-1 text-xs ${className}`}>{state}</div>
  )
}
