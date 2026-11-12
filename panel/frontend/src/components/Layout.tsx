import { useState } from "react";
import {
  LayoutDashboard, ArrowLeftRight, Server, Gauge, Users, KeyRound, LogOut, LineChart, Waypoints,
  Menu, Sun, Moon, Monitor, ChevronsUpDown, type LucideIcon,
} from "lucide-react";
import { cn, initials } from "@/lib/utils";
import { api, type User } from "@/api";
import { useHashRoute } from "@/lib/route";
import type { ThemeApi, ThemePref } from "@/lib/theme";
import { BrandMark } from "@/components/brand";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetTitle, SheetDescription } from "@/components/ui/sheet";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuRadioGroup,
  DropdownMenuRadioItem, DropdownMenuSeparator, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Tooltip } from "@/components/ui/tooltip";
import { Dashboard } from "@/views/Dashboard";
import { Traffic } from "@/views/Traffic";
import { Rules } from "@/views/Rules";
import { Topology } from "@/views/Topology";
import { Nodes } from "@/views/Nodes";
import { System } from "@/views/System";
import { UsersView } from "@/views/Users";
import { PasswordDialog } from "@/views/Password";

const VIEWS = ["dashboard", "traffic", "rules", "topology", "nodes", "system", "users"] as const;
type View = (typeof VIEWS)[number];

interface NavItem { key: View; label: string; icon: LucideIcon; adminOnly?: boolean }
const NAV: { group: string; items: NavItem[] }[] = [
  { group: "概览", items: [
    { key: "dashboard", label: "仪表盘", icon: LayoutDashboard },
    { key: "traffic", label: "流量统计", icon: LineChart },
  ] },
  { group: "转发", items: [
    { key: "rules", label: "转发规则", icon: ArrowLeftRight },
    { key: "topology", label: "转发拓扑", icon: Waypoints },
  ] },
  { group: "基础设施", items: [
    { key: "nodes", label: "节点", icon: Server, adminOnly: true },
    { key: "system", label: "系统调优", icon: Gauge, adminOnly: true },
  ] },
  { group: "管理", items: [
    { key: "users", label: "用户", icon: Users, adminOnly: true },
  ] },
];

const THEME_ICON: Record<ThemePref, LucideIcon> = { light: Sun, dark: Moon, system: Monitor };

function UserMenu({ user, theme, onPassword, onLogout }: { user: User; theme: ThemeApi; onPassword: () => void; onLogout: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button className="flex w-full items-center gap-3 rounded-lg p-2 text-left transition-colors hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 data-[state=open]:bg-accent">
          <div className="grid h-8 w-8 shrink-0 place-items-center rounded-full bg-gradient-to-br from-primary/80 to-violet-500/80 text-[13px] font-semibold text-white">{initials(user.username)}</div>
          <div className="min-w-0 flex-1">
            <div className="truncate text-[13px] font-medium">{user.username}</div>
            <div className="text-[11px] text-muted-foreground">{user.is_admin ? "管理员" : "普通用户"}</div>
          </div>
          <ChevronsUpDown className="h-4 w-4 text-muted-foreground" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start" className="w-[--radix-dropdown-menu-trigger-width] min-w-56">
        <DropdownMenuLabel>外观</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={theme.pref} onValueChange={(v) => theme.setTheme(v as ThemePref)}>
          <DropdownMenuRadioItem value="light"><Sun />浅色</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="dark"><Moon />深色</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="system"><Monitor />跟随系统</DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onPassword}><KeyRound />修改密码</DropdownMenuItem>
        <DropdownMenuItem destructive onSelect={onLogout}><LogOut />退出登录</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function SidebarContent({ view, onNavigate, admin, version, footer }: { view: View; onNavigate: (v: View) => void; admin: boolean; version: string; footer: React.ReactNode }) {
  return (
    <div className="flex h-full flex-col">
      <div className="flex h-16 items-center gap-2.5 px-5">
        <BrandMark />
        <div className="min-w-0">
          <div className="text-[15px] font-semibold leading-tight tracking-tight">Relay Panel</div>
          <div className="truncate text-[11px] text-muted-foreground">{version.split(" (")[0] || "转发控制台"}</div>
        </div>
      </div>
      <nav className="flex-1 space-y-5 overflow-y-auto px-3 py-3">
        {NAV.map(({ group, items }) => {
          const visible = items.filter((n) => !n.adminOnly || admin);
          if (!visible.length) return null;
          return (
            <div key={group}>
              <div className="mb-1 px-3 text-[11px] font-medium uppercase tracking-wider text-muted-foreground/70">{group}</div>
              <div className="space-y-0.5">
                {visible.map((n) => {
                  const active = view === n.key;
                  return (
                    <button
                      key={n.key}
                      onClick={() => onNavigate(n.key)}
                      aria-current={active ? "page" : undefined}
                      className={cn(
                        "group relative flex w-full items-center gap-3 rounded-lg px-3 py-2 text-[13.5px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
                        active ? "bg-primary/10 font-medium text-primary" : "text-muted-foreground hover:bg-accent hover:text-foreground"
                      )}
                    >
                      {active && <span className="absolute -left-3 top-1/2 h-5 w-1 -translate-y-1/2 rounded-r-full bg-primary" />}
                      <n.icon className={cn("h-[18px] w-[18px]", active ? "text-primary" : "text-muted-foreground group-hover:text-foreground")} />
                      {n.label}
                    </button>
                  );
                })}
              </div>
            </div>
          );
        })}
      </nav>
      <div className="border-t p-3">{footer}</div>
    </div>
  );
}

export function Layout({ user, version, theme, onLogout }: { user: User; version: string; theme: ThemeApi; onLogout: () => void }) {
  const admin = user.is_admin;
  const [view, navigate] = useHashRoute(VIEWS, "dashboard");
  const [mobileOpen, setMobileOpen] = useState(false);
  const [pwOpen, setPwOpen] = useState(false);

  const allowed = NAV.flatMap((g) => g.items).find((n) => n.key === view);
  const current: View = allowed && (!allowed.adminOnly || admin) ? view : "dashboard";
  const currentLabel = NAV.flatMap((g) => g.items).find((n) => n.key === current)?.label;

  const logout = async () => { await api("/api/logout", { method: "POST" }).catch(() => {}); onLogout(); };
  const go = (v: View) => { navigate(v); setMobileOpen(false); window.scrollTo({ top: 0 }); };
  const footer = <UserMenu user={user} theme={theme} onPassword={() => { setMobileOpen(false); setPwOpen(true); }} onLogout={logout} />;
  const ThemeIcon = THEME_ICON[theme.resolved === "dark" ? "dark" : "light"];

  return (
    <div className="min-h-screen">
      <aside className="fixed inset-y-0 left-0 z-40 hidden w-60 border-r bg-sidebar lg:block">
        <SidebarContent view={current} onNavigate={go} admin={admin} version={version} footer={footer} />
      </aside>

      <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
        <SheetContent side="left" className="w-72 bg-sidebar p-0">
          <SheetTitle className="sr-only">导航</SheetTitle>
          <SheetDescription className="sr-only">切换页面</SheetDescription>
          <SidebarContent view={current} onNavigate={go} admin={admin} version={version} footer={footer} />
        </SheetContent>
      </Sheet>

      <div className="lg:pl-60">
        <header className="sticky top-0 z-30 flex h-14 items-center gap-3 border-b bg-background/80 px-4 backdrop-blur-md supports-[backdrop-filter]:bg-background/60 lg:px-8">
          <Button variant="ghost" size="icon-sm" className="lg:hidden" onClick={() => setMobileOpen(true)}>
            <Menu /><span className="sr-only">打开导航</span>
          </Button>
          <div className="flex min-w-0 items-center gap-2 text-[13px]">
            <span className="text-muted-foreground max-sm:hidden">控制台</span>
            <span className="text-muted-foreground/50 max-sm:hidden">/</span>
            <span className="truncate font-medium">{currentLabel}</span>
          </div>
          <div className="ml-auto flex items-center gap-1.5">
            <div className="mr-2 hidden items-center gap-2 rounded-full border bg-card px-2.5 py-1 text-[11.5px] text-muted-foreground sm:flex">
              <span className="relative flex h-1.5 w-1.5"><span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-success opacity-70" /><span className="relative h-1.5 w-1.5 rounded-full bg-success" /></span>
              实时同步
            </div>
            <Tooltip content={theme.resolved === "dark" ? "切换到浅色" : "切换到深色"}>
              <Button variant="ghost" size="icon-sm" onClick={() => theme.setTheme(theme.resolved === "dark" ? "light" : "dark")}>
                <ThemeIcon /><span className="sr-only">切换主题</span>
              </Button>
            </Tooltip>
          </div>
        </header>

        <main className="mx-auto w-full max-w-[1400px] px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
          {current === "dashboard" && <Dashboard isAdmin={admin} onNavigate={(v) => go(v as View)} />}
          {current === "traffic" && <Traffic />}
          {current === "rules" && <Rules isAdmin={admin} />}
          {current === "topology" && <Topology />}
          {current === "nodes" && <Nodes />}
          {current === "system" && <System />}
          {current === "users" && <UsersView meId={user.id} />}
        </main>
      </div>

      <PasswordDialog open={pwOpen} onOpenChange={setPwOpen} />
    </div>
  );
}
