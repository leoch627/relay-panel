// 面板 API 客户端：与后端 JSON 契约一一对应
export interface User { id: number; username: string; is_admin: boolean; created_at: string }
export interface UnitStatus { active: string; enabled: string; started_at?: string; restarts?: string; main_pid?: string }
export interface ProbeResult { ok: boolean; ms: number; at: string; err?: string; soft?: boolean }
export interface RuleStats { up: number; down: number; conns: number; conns_total: number; udp: number; udp_total: number; rate_kbps: number; t: number; over_quota?: boolean }
export interface Rule {
  tag: string; listen_ip: string; listen_port: number; remote_host: string;
  remote_tcp_port: number; remote_udp_port: number; enable_tcp: boolean; enable_udp: boolean;
  advanced?: Record<string, number>; remark: string; node_id: number; desired_state: string;
  rate_limit_kbps: number; traffic_quota_bytes: number; over_quota: boolean;
  status: UnitStatus; stats: RuleStats; probe?: ProbeResult | null; owner: string; node_name: string; my_rule: boolean;
}
export interface NodeInfo {
  id: number; name: string; key?: string; online: boolean; last_seen: string; rule_count: number;
  report: { at: string; machine: MachineInfo; tcp_pool: string; panel: string; statuses: Record<string, UnitStatus>; stats?: Record<string, RuleStats> };
}
export interface MachineInfo { kernel?: string; hostname?: string; os?: string; cpu_cores?: number; load_avg?: string; mem_total?: number; mem_avail?: number }
export interface TuningStatus {
  kernel: string; congestion_control: string; available_cc: string[]; qdisc: string;
  bbr_supported: boolean; bbr_active: boolean; config_file: string; file_managed: boolean; file_exists: boolean;
  profiles: { name: string; label: string; desc: string }[];
}
export interface TrafficPoint { t: number; up: number; down: number }
export interface TrafficRow { tag: string; node_id: number; up: number; down: number; conns: number }
export interface TrafficData { range: string; points: TrafficPoint[]; rows: TrafficRow[]; heatmap: { date: string; up: number; down: number }[]; totals: { up: number; down: number; conns: number } }
export interface Overview {
  rules_total: number; rules_running: number; rules_stopped: number;
  nodes_total: number; nodes_online: number; users_total: number;
  traffic_total: number; over_quota: string[]; machine: MachineInfo; version: string; tuning?: TuningStatus;
}

export type ApiError = { error: string };
let onUnauthorized: (() => void) | null = null;
export function setOnUnauthorized(fn: () => void) { onUnauthorized = fn; }

export async function api<T = any>(path: string, opts: { method?: string; body?: any } = {}): Promise<T> {
  const res = await fetch(path, {
    method: opts.method || (opts.body ? "POST" : "GET"),
    headers: opts.body ? { "Content-Type": "application/json" } : undefined,
    body: opts.body ? JSON.stringify(opts.body) : undefined,
  });
  const data = await res.json().catch(() => ({}) as any);
  if (res.status === 401) {
    if (onUnauthorized) onUnauthorized();
    throw new Error(data.error || "未登录");
  }
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
  return data as T;
}

// 高级参数元数据（与后端取值范围一致）
export const ADV: [string, string, number, string][] = [
  ["POOL_SIZE", "预连接池大小（0 关闭）", 24, "0-256"],
  ["REFILL_BATCH", "每轮补连接并发数", 8, "1-256"],
  ["CONNECT_TIMEOUT", "出站连接超时(秒)", 5, "1-120"],
  ["IDLE_TIMEOUT", "空闲连接回收(秒)", 240, "30-86400"],
  ["HALF_CLOSE_TIMEOUT", "半关闭保留(秒)", 10, "1-300"],
  ["PRECONNECT_TTL_MS", "预连接轮换(毫秒)", 50000, "10000-3600000"],
  ["SPLICE_CHUNK", "splice 缓冲(字节)", 262144, "16384-1048576"],
  ["UDP_IDLE_TIMEOUT", "UDP 会话回收(秒)", 60, "5-3600"],
  ["UDP_SOCKET_BUFFER", "UDP 缓冲(字节)", 4194304, "65536-67108864"],
  ["LISTEN_BACKLOG", "listen backlog", 16384, "128-65535"],
  ["LOG_ENABLE", "日志开关(1/0)", 1, "0-1"],
  ["LOG_RATE_PER_SEC", "每秒日志上限", 24, "0-10000"],
  ["TCP_KEEPIDLE", "keepalive 起始(秒)", 360, "30-86400"],
  ["TCP_KEEPINTVL", "keepalive 间隔(秒)", 15, "1-3600"],
  ["TCP_KEEPCNT", "keepalive 次数", 1, "1-30"],
  ["TCP_USER_TIMEOUT_MS", "未确认数据等待(ms)", 0, "0-3600000"],
];

export function statusBadgeVariant(active?: string): { variant: string; text: string } {
  switch (active) {
    case "active": return { variant: "success", text: "运行中" };
    case "failed": return { variant: "destructive", text: "失败" };
    case "inactive": return { variant: "secondary", text: "已停止" };
    case "activating": return { variant: "warning", text: "启动中" };
    case "pending": return { variant: "warning", text: "待节点上报" };
    case "node-offline": return { variant: "destructive", text: "节点离线" };
    default: return { variant: "secondary", text: active || "未知" };
  }
}
