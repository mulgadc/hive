import { useQueryClient } from "@tanstack/react-query"
import { Link, useLocation, useNavigate } from "@tanstack/react-router"
import {
  Camera,
  HardDrive,
  Home,
  Image,
  Key,
  LayoutGrid,
  LogOut,
  Network,
  Server,
} from "lucide-react"

// import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
// import {
//   ChevronsUpDown,
// } from "lucide-react"
// import {
//   DropdownMenu,
//   DropdownMenuContent,
//   DropdownMenuGroup,
//   DropdownMenuItem,
//   DropdownMenuTrigger,
// } from "@/components/ui/dropdown-menu"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
} from "@/components/ui/sidebar"
import { clearCredentials } from "@/lib/auth"
import { clearClients } from "@/lib/awsClient"

export function SidebarLayout() {
  const pathname = useLocation({
    select: (location) => location.pathname,
  })
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  function handleLogout() {
    clearCredentials()
    clearClients()
    queryClient.clear()
    navigate({ to: "/login" })
  }

  return (
    <Sidebar collapsible="icon">
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>General</SidebarGroupLabel>
          <SidebarMenu>
            <SidebarMenuItem>
              <Link to="/">
                <SidebarMenuButton
                  isActive={pathname === "/"}
                  tooltip="Dashboard"
                >
                  <Home className="size-4" />
                  <span>Dashboard</span>
                </SidebarMenuButton>
              </Link>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroup>

        <SidebarGroup>
          <SidebarGroupLabel>EC2</SidebarGroupLabel>
          <SidebarMenu>
            <SidebarMenuItem>
              <Link to="/ec2/describe-instances">
                <SidebarMenuButton
                  isActive={
                    pathname.startsWith("/ec2/describe-instances") ||
                    pathname.startsWith("/ec2/run-instances")
                  }
                  tooltip="EC2 Instances"
                >
                  <Server className="size-4" />
                  <span>Instances</span>
                </SidebarMenuButton>
              </Link>
            </SidebarMenuItem>

            <SidebarMenuItem>
              <Link to="/ec2/describe-images">
                <SidebarMenuButton
                  isActive={pathname.startsWith("/ec2/describe-images")}
                  tooltip="Images"
                >
                  <Image className="size-4" />
                  <span>Images</span>
                </SidebarMenuButton>
              </Link>
            </SidebarMenuItem>

            <SidebarMenuItem>
              <Link to="/ec2/describe-key-pairs">
                <SidebarMenuButton
                  isActive={pathname.startsWith("/ec2/describe-key-pairs")}
                  tooltip="Key Pairs"
                >
                  <Key className="size-4" />
                  <span>Key Pairs</span>
                </SidebarMenuButton>
              </Link>
            </SidebarMenuItem>

            <SidebarMenuItem>
              <Link to="/ec2/describe-volumes">
                <SidebarMenuButton
                  isActive={
                    pathname.startsWith("/ec2/describe-volumes") ||
                    pathname.startsWith("/ec2/create-volume") ||
                    pathname.startsWith("/ec2/modify-volume")
                  }
                  tooltip="Volumes"
                >
                  <HardDrive className="size-4" />
                  <span>Volumes</span>
                </SidebarMenuButton>
              </Link>
            </SidebarMenuItem>

            <SidebarMenuItem>
              <Link to="/ec2/describe-snapshots">
                <SidebarMenuButton
                  isActive={
                    pathname.startsWith("/ec2/describe-snapshots") ||
                    pathname.startsWith("/ec2/create-snapshot")
                  }
                  tooltip="Snapshots"
                >
                  <Camera className="size-4" />
                  <span>Snapshots</span>
                </SidebarMenuButton>
              </Link>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroup>

        <SidebarGroup>
          <SidebarGroupLabel>Networking</SidebarGroupLabel>
          <SidebarMenu>
            <SidebarMenuItem>
              <Link to="/ec2/describe-vpcs">
                <SidebarMenuButton
                  isActive={
                    pathname.startsWith("/ec2/describe-vpcs") ||
                    pathname.startsWith("/ec2/create-vpc")
                  }
                  tooltip="VPCs"
                >
                  <Network className="size-4" />
                  <span>VPCs</span>
                </SidebarMenuButton>
              </Link>
            </SidebarMenuItem>

            <SidebarMenuItem>
              <Link to="/ec2/describe-subnets">
                <SidebarMenuButton
                  isActive={
                    pathname.startsWith("/ec2/describe-subnets") ||
                    pathname.startsWith("/ec2/create-subnet")
                  }
                  tooltip="Subnets"
                >
                  <LayoutGrid className="size-4" />
                  <span>Subnets</span>
                </SidebarMenuButton>
              </Link>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroup>

        <SidebarGroup>
          <SidebarGroupLabel>S3</SidebarGroupLabel>
          <SidebarMenu>
            <SidebarMenuItem>
              <Link to="/s3/ls">
                <SidebarMenuButton
                  isActive={pathname.startsWith("/s3/ls")}
                  tooltip="Buckets"
                >
                  <Server className="size-4" />
                  <span>Buckets</span>
                </SidebarMenuButton>
              </Link>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarGroup>
      </SidebarContent>

      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton onClick={handleLogout} tooltip="Sign out">
              <LogOut className="size-4" />
              <span>Sign out</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>

      {/* Old avatar dropdown menu - commented out
      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem>
            <DropdownMenu>
              <DropdownMenuTrigger
                render={(props) => (
                  <SidebarMenuButton
                    className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
                    size="lg"
                    {...props}
                  >
                    <Avatar className="h-8 w-8 rounded-lg">
                      <AvatarImage alt="Mulga Hive" src="/favicon.ico" />
                      <AvatarFallback className="rounded-lg">MH</AvatarFallback>
                    </Avatar>
                    <div className="grid flex-1 text-left text-sm leading-tight">
                      <span className="truncate font-semibold">Mulga Hive</span>
                      <span className="truncate text-xs">hive@mulgadc.com</span>
                    </div>
                    <ChevronsUpDown className="ml-auto size-4" />
                  </SidebarMenuButton>
                )}
              />
              <DropdownMenuContent
                align="end"
                className="w-(--radix-dropdown-menu-trigger-width) min-w-56 rounded-lg"
                side={isMobile ? "bottom" : "right"}
                sideOffset={4}
              >
                <DropdownMenuGroup>
                  <DropdownMenuItem>
                    <LogOut className="size-4" />
                    Sign out
                  </DropdownMenuItem>
                </DropdownMenuGroup>
              </DropdownMenuContent>
            </DropdownMenu>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
      */}
      <SidebarRail />
    </Sidebar>
  )
}
