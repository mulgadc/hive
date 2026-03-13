import { cn } from "@/lib/utils"

const SUCCESS_STATES = new Set(["running", "available", "completed", "Active"])
const ERROR_STATES = ["stopped", "error"]
const WARNING_STATES = ["pending", "shutting-down", "stopping"]

function getStateClass(state: string | undefined): string {
  if (state && SUCCESS_STATES.has(state)) {
    return "bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-100"
  }
  if (state && ERROR_STATES.includes(state)) {
    return "bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-100"
  }
  if (state && WARNING_STATES.includes(state)) {
    return "bg-yellow-100 text-yellow-800 dark:bg-yellow-900 dark:text-yellow-100"
  }
  if (state === "terminated") {
    return "bg-zinc-100 text-zinc-500 dark:bg-zinc-800 dark:text-zinc-400"
  }
  return "bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-100"
}

export function StateBadge({ state }: { state: string | undefined }) {
  return (
    <div className={cn("rounded-full px-2 py-1 text-xs", getStateClass(state))}>
      {state}
    </div>
  )
}
