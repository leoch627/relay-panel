import type { Rule } from "@/api";

export type Tone = "success" | "warning" | "destructive" | "secondary" | "info";
export interface RuleState { label: string; tone: Tone; live?: boolean; group: "running" | "stopped" | "issue" }

// 把实例实际状态、期望状态、配额合并成一个对用户有意义的状态
export function ruleState(r: Pick<Rule, "status" | "desired_state" | "over_quota" | "stats">): RuleState {
  const desired = r.desired_state || "running";
  if (r.over_quota || r.stats?.over_quota) return { label: "超出配额", tone: "destructive", group: "issue" };
  // 已停止的规则即使所在节点离线也不算异常
  if (desired === "stopped" && r.status?.active === "node-offline") return { label: "已停止", tone: "secondary", group: "stopped" };
  if (r.status?.active === "node-offline") return { label: "节点离线", tone: "destructive", group: "issue" };
  if (desired === "stopped") {
    return r.status?.active === "active"
      ? { label: "停止中", tone: "warning", group: "stopped" }
      : { label: "已停止", tone: "secondary", group: "stopped" };
  }
  switch (r.status?.active) {
    case "active": return { label: "运行中", tone: "success", live: true, group: "running" };
    case "failed": return { label: "启动失败", tone: "destructive", group: "issue" };
    case "activating": return { label: "启动中", tone: "warning", group: "running" };
    case "pending": return { label: "等待节点", tone: "warning", group: "running" };
    case "inactive": return { label: "未运行", tone: "warning", group: "issue" };
    default: return { label: r.status?.active && r.status.active !== "unknown" ? r.status.active : "状态未知", tone: "secondary", group: "running" };
  }
}

export function isHalted(r: Rule) {
  return (r.desired_state || "running") !== "running" || r.over_quota || !!r.stats?.over_quota;
}

export type ProbeView = { label: string; tone: Tone; ms?: number } | null;

export function probeView(r: Rule): ProbeView {
  const p = r.probe;
  if (!p) return null;
  if (p.ok) return { label: `${p.ms} ms`, tone: p.ms <= 150 ? "success" : p.ms <= 300 ? "warning" : "destructive", ms: p.ms };
  return p.soft ? { label: "无响应", tone: "warning" } : { label: "不可达", tone: "destructive" };
}

export const toneText: Record<Tone, string> = {
  success: "text-success", warning: "text-warning", destructive: "text-destructive", secondary: "text-muted-foreground", info: "text-info",
};
export const toneBg: Record<Tone, string> = {
  success: "bg-success", warning: "bg-warning", destructive: "bg-destructive", secondary: "bg-muted-foreground/50", info: "bg-info",
};
