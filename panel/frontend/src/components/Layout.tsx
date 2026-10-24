import { useState } from "react";
import {
  LayoutDashboard, ArrowLeftRight, Network, Settings2, Users, KeyRound, LogOut, LineChart, Workflow,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { api, type User } from "@/api";
import { Dashboard } from "@/views/Dashboard";
import { Traffic } from "@/views/Traffic";
import { Rules } from "@/views/Rules";
import { Topology } from "@/views/Topology";
import { Nodes } from "@/views/Nodes";
import { System } from "@/views/System";
import { UsersView } from "@/views/Users";
import { Password } from "@/views/Password";

type View = "dashboard" | "traffic" | "rules" | "topology" | "nodes" | "system" | "users" | "password";

export function Layout({ user, version, onLogout }: { user: User; version: string; onLogout: () => void }) {
  const [view, setView] = useState<View>("dashboard");
  const admin = user.is_admin;

  const nav: { key: View; label: string; icon: any; adminOnly?: boolean }[] = [
    { key: "dashboard", label: "仪表盘", icon: LayoutDashboard },
    { key: "traffic", label: "流量统计", icon: LineChart },
    { key: "rules", label: "转发管理", icon: ArrowLeftRight },
    { key: "topology", label: "转发拓扑", icon: Workflow },
    { key: "nodes", label: "节点管理", icon: Network, adminOnly: true },
    { key: "system", label: "系统调优", icon: Settings2, adminOnly: true },
    { key: "users", label: "用户管理", icon: Users, adminOnly: true },
    { key: "password", label: "修改密码", icon: KeyRound },
  ];

  return (
    <div className="flex h-full min-h-screen">
      <aside className="flex w-52 shrink-0 flex-col gap-1 border-r bg-popover p-3 max-md:w-full max-md:flex-row max-md:overflow-x-auto max-md:border-b max-md:border-r-0">
        <div className="px-3 pb-4 pt-2 max-md:px-2 max-md:pb-2 max-md:whitespace-nowrap">
          <div className="text-[15px] font-semibold tracking-tight">Relay Panel</div>
          <div className="text-[10.5px] text-muted-foreground">{version}</div>
        </div>
        {nav.filter((n) => !n.adminOnly || admin).map((n) => (
          <div
            key={n.key}
            onClick={() => setView(n.key)}
            className={cn(
              "flex cursor-pointer select-none items-center gap-2.5 rounded-md px-3 py-2 text-[13.5px] transition-colors max-md:whitespace-nowrap",
              view === n.key ? "bg-muted font-medium text-foreground" : "text-muted-foreground hover:bg-accent hover:text-accent-foreground"
            )}
          >
            <n.icon className="h-4 w-4" />
            {n.label}
          </div>
        ))}
        <div className="flex-1 max-md:hidden" />
        <div className="border-t pt-3 text-xs text-muted-foreground max-md:border-l max-md:border-t-0 max-md:pl-3 max-md:pt-0 max-md:leading-9">
          <div className="mb-1 max-md:hidden">
            {user.username} {admin && <span className="rounded border border-primary/30 bg-primary/10 px-1.5 py-px text-[10px] text-blue-400">管理员</span>}
          </div>
          <a
            href="javascript:void(0)"
            className="flex items-center gap-1.5 hover:text-foreground"
            onClick={async () => { await api("/api/logout", { method: "POST" }).catch(() => {}); onLogout(); }}
          >
            <LogOut className="h-3.5 w-3.5" />退出登录
          </a>
        </div>
      </aside>
      <main className="max-w-6xl flex-1 p-6 max-md:p-4">
        {view === "dashboard" && <Dashboard isAdmin={admin} />}
        {view === "traffic" && <Traffic />}
        {view === "rules" && <Rules isAdmin={admin} />}
        {view === "topology" && <Topology />}
        {view === "nodes" && <Nodes />}
        {view === "system" && <System />}
        {view === "users" && <UsersView meId={user.id} />}
        {view === "password" && <Password />}
      </main>
    </div>
  );
}
