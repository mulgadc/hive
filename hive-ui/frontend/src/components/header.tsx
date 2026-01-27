import { ThemeSwitch } from "@/components/theme-switch"
import { SidebarTrigger } from "@/components/ui/sidebar"
import { cn } from "@/lib/utils"

export function Header() {
  return (
    <header
      className={cn(
        "z-50 flex h-10 shrink-0 items-center gap-2 border-sidebar-border border-b bg-sidebar px-4 text-sidebar-foreground",
        "sticky top-0",
      )}
    >
      <SidebarTrigger className="-ml-1" />
      <div className="flex w-full justify-end">
        <ThemeSwitch />
      </div>
    </header>
  )
}
