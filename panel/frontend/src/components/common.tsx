import { useState, type ReactNode } from "react";
import { toast } from "sonner";
import { AlertCircle, Check, Copy, type LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import { toneBg, type Tone } from "@/lib/ruleState";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import type { NodeInfo } from "@/api";

export function PageHeader({ title, description, actions, children }: { title: string; description?: ReactNode; actions?: ReactNode; children?: ReactNode }) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-x-6 gap-y-3">
      <div className="min-w-0">
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {description && <p className="mt-1.5 max-w-2xl text-[13px] leading-relaxed text-muted-foreground">{description}</p>}
        {children}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

export function StatusDot({ tone, live, className }: { tone: Tone; live?: boolean; className?: string }) {
  return (
    <span className={cn("relative inline-flex h-2 w-2 shrink-0", className)}>
      {live && <span className={cn("absolute inline-flex h-full w-full animate-ping rounded-full opacity-60", toneBg[tone])} />}
      <span className={cn("relative inline-flex h-2 w-2 rounded-full", toneBg[tone])} />
    </span>
  );
}

export function StatusLabel({ tone, live, children, className }: { tone: Tone; live?: boolean; children: ReactNode; className?: string }) {
  return (
    <span className={cn("inline-flex items-center gap-2 whitespace-nowrap text-[13px]", className)}>
      <StatusDot tone={tone} live={live} />
      {children}
    </span>
  );
}

export function ErrorBanner({ message, className }: { message?: string | null; className?: string }) {
  if (!message) return null;
  return (
    <div role="status" className={cn("mb-4 flex items-center gap-2 rounded-lg border border-destructive/25 bg-destructive/5 px-3 py-2 text-[13px] text-destructive", className)}>
      <AlertCircle className="h-4 w-4 shrink-0" />
      <span>刷新失败：{message}，稍后自动重试</span>
    </div>
  );
}

export function EmptyState({ icon: Icon, title, description, action, className }: { icon: LucideIcon; title: string; description?: ReactNode; action?: ReactNode; className?: string }) {
  return (
    <div className={cn("flex flex-col items-center justify-center px-6 py-14 text-center", className)}>
      <div className="mb-4 grid h-12 w-12 place-items-center rounded-xl border bg-muted/50 text-muted-foreground shadow-sm">
        <Icon className="h-5 w-5" />
      </div>
      <div className="text-sm font-medium">{title}</div>
      {description && <p className="mt-1.5 max-w-sm text-[13px] leading-relaxed text-muted-foreground">{description}</p>}
      {action && <div className="mt-5">{action}</div>}
    </div>
  );
}

export function LoadingState({ label = "加载中…", error }: { label?: string; error?: string | null }) {
  return (
    <div role="status" className="flex items-center justify-center gap-2.5 py-24 text-[13px] text-muted-foreground">
      {!error && <span className="h-4 w-4 animate-spin rounded-full border-2 border-muted-foreground/30 border-t-primary" />}
      {error ? <span className="text-destructive">{error}</span> : label}
    </div>
  );
}

export async function copyText(text: string) {
  try { await navigator.clipboard.writeText(text); toast.success("已复制到剪贴板"); return true; }
  catch { toast.error("复制失败，请手动选择文本"); return false; }
}

export function CopyButton({ text, className, label }: { text: string; className?: string; label?: string }) {
  const [done, setDone] = useState(false);
  return (
    <Button
      type="button"
      size={label ? "xs" : "icon-sm"}
      variant="ghost"
      className={cn(label ? "" : "h-7 w-7", className)}
      onClick={async () => { if (await copyText(text)) { setDone(true); setTimeout(() => setDone(false), 1500); } }}
    >
      {done ? <Check className="text-success" /> : <Copy />}
      {label}
      <span className="sr-only">复制</span>
    </Button>
  );
}

export function CodeBlock({ code, className }: { code: string; className?: string }) {
  return (
    <div className={cn("group relative rounded-lg border bg-zinc-950 text-zinc-200 dark:bg-black/40", className)}>
      <div className="absolute right-2 top-2">
        <CopyButton text={code} className="text-zinc-400 hover:bg-white/10 hover:text-white" />
      </div>
      <pre className="max-h-[50vh] overflow-auto whitespace-pre-wrap break-all p-4 pr-12 font-mono text-xs leading-relaxed">{code}</pre>
    </div>
  );
}

export function StatCard({ icon: Icon, label, value, hint, accent = "primary", children, className }: {
  icon: LucideIcon; label: string; value: ReactNode; hint?: ReactNode; accent?: "primary" | "success" | "warning" | "info" | "chart-2" | "destructive";
  children?: ReactNode; className?: string;
}) {
  const accents = {
    primary: "bg-primary/10 text-primary", success: "bg-success/10 text-success", warning: "bg-warning/10 text-warning",
    info: "bg-info/10 text-info", "chart-2": "bg-chart-2/10 text-chart-2", destructive: "bg-destructive/10 text-destructive",
  };
  return (
    <Card className={cn("p-5", className)}>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="text-[13px] text-muted-foreground">{label}</div>
          <div className="tabular mt-2 truncate text-2xl font-semibold tracking-tight">{value}</div>
        </div>
        <div className={cn("grid h-9 w-9 shrink-0 place-items-center rounded-lg", accents[accent])}><Icon className="h-[18px] w-[18px]" /></div>
      </div>
      {hint && <div className="mt-1.5 text-xs text-muted-foreground">{hint}</div>}
      {children}
    </Card>
  );
}

export function nodeOptionLabel(n: NodeInfo) {
  return n.deleting ? "清理中" : n.online ? "在线" : "离线";
}

export function nodeTone(n: Pick<NodeInfo, "online" | "deleting">): Tone {
  return n.deleting ? "warning" : n.online ? "success" : "destructive";
}
