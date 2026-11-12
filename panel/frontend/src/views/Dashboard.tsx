import { useCallback, useMemo } from "react";
import {
  AlertTriangle, ArrowLeftRight, ArrowRight, CheckCircle2, Cpu, Database, Gauge, HardDrive, Server, Activity, WifiOff, Timer, Users,
} from "lucide-react";
import { api, type NodeInfo, type Overview, type Rule, type TrafficData } from "@/api";
import { cn, fmtBytes, fmtRelative } from "@/lib/utils";
import { usePolling } from "@/lib/usePolling";
import { ruleState } from "@/lib/ruleState";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Meter, Skeleton } from "@/components/ui/misc";
import { ErrorBanner, LoadingState, PageHeader, StatCard, StatusDot, nodeTone } from "@/components/common";
import { ChartLegend, TrafficChart } from "@/components/traffic-chart";

interface Attention { key: string; tone: "destructive" | "warning"; icon: typeof AlertTriangle; title: string; desc: string; to: string }

export function Dashboard({ isAdmin, onNavigate }: { isAdmin: boolean; onNavigate: (v: string) => void }) {
  const overviewReq = useCallback((signal: AbortSignal) => api<Overview>("/api/overview", { signal }), []);
  const rulesReq = useCallback((signal: AbortSignal) => api<{ rules: Rule[] }>("/api/rules", { signal }).then((d) => d.rules || []), []);
  const trafficReq = useCallback((signal: AbortSignal) => api<TrafficData>("/api/traffic?range=24h", { signal }), []);
  // 普通用户看不到节点页，不拉取节点列表
  const nodesReq = useCallback((signal: AbortSignal) =>
    isAdmin ? api<{ nodes: NodeInfo[] }>("/api/nodes", { signal }).then((d) => d.nodes || []) : Promise.resolve([] as NodeInfo[]), [isAdmin]);
  const { data: d, error } = usePolling(overviewReq);
  const { data: rules } = usePolling(rulesReq, 10000);
  const { data: traffic } = usePolling(trafficReq, 60000);
  const { data: nodes } = usePolling(nodesReq, isAdmin ? 10000 : null);

  const attention = useMemo(() => {
    const items: Attention[] = [];
    for (const r of rules || []) {
      const st = ruleState(r);
      if (st.label === "超出配额") items.push({ key: `q-${r.tag}`, tone: "destructive", icon: Database, title: `${r.tag} 已达流量配额`, desc: "实例已自动停机，可编辑配额后重新启动", to: "rules" });
      else if (st.group === "issue" && st.label !== "节点离线") items.push({ key: `s-${r.tag}`, tone: st.tone === "destructive" ? "destructive" : "warning", icon: AlertTriangle, title: `${r.tag} ${st.label}`, desc: `${r.node_name} · 监听 :${r.listen_port}`, to: "rules" });
      else if (r.probe && !r.probe.ok && !r.probe.soft && (r.desired_state || "running") === "running") items.push({ key: `p-${r.tag}`, tone: "warning", icon: Timer, title: `${r.tag} 落地探测失败`, desc: `${r.remote_host} 不可达${r.probe.err ? `：${r.probe.err}` : ""}`, to: "topology" });
    }
    for (const n of nodes || []) {
      if (!n.online && !n.deleting) items.push({ key: `n-${n.id}`, tone: "destructive", icon: WifiOff, title: `节点 ${n.name} 离线`, desc: `最近心跳 ${fmtRelative(n.last_seen)} · 承载 ${n.rule_count} 条转发`, to: "nodes" });
    }
    return items;
  }, [rules, nodes]);

  const topRules = useMemo(() => [...(traffic?.rows || [])].sort((a, b) => b.up + b.down - (a.up + a.down)).slice(0, 5), [traffic]);

  if (!d) return <LoadingState error={error} />;
  const m = d.machine; // 仅管理员返回
  const memUsed = m?.mem_total ? m.mem_total - (m.mem_avail || 0) : 0;
  const memPct = m?.mem_total ? (memUsed / m.mem_total) * 100 : 0;
  const load = (m?.load_avg || "").split(/\s+/).filter(Boolean);
  const t = d.tuning;
  const runPct = d.rules_total ? (d.rules_running / d.rules_total) * 100 : 0;
  const total24 = traffic ? traffic.totals.up + traffic.totals.down : null;
  const topMax = Math.max(1, ...topRules.map((r) => r.up + r.down));

  return (
    <div>
      <PageHeader
        title="仪表盘"
        description="转发、节点与流量的实时概况"
        actions={<Button size="sm" onClick={() => onNavigate("rules")}><ArrowLeftRight />管理转发</Button>}
      />
      <ErrorBanner message={error} />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard icon={ArrowLeftRight} label="转发规则" value={<>{d.rules_running}<span className="ml-1 text-base font-normal text-muted-foreground">/ {d.rules_total}</span></>} hint={`${d.rules_stopped} 条已停止`}>
          <Meter value={runPct} tone="primary" className="mt-3" />
        </StatCard>
        <StatCard icon={Server} accent="success" label="在线节点" value={<>{d.nodes_online}<span className="ml-1 text-base font-normal text-muted-foreground">/ {d.nodes_total}</span></>}
          hint={d.nodes_total === 0 ? "尚未接入被控节点" : d.nodes_online === d.nodes_total ? "全部节点在线" : `${d.nodes_total - d.nodes_online} 个节点离线`} />
        <StatCard icon={Activity} accent="chart-2" label="近 24 小时流量" value={total24 === null ? <Skeleton className="h-8 w-24" /> : fmtBytes(total24)}
          hint={traffic ? `↓ ${fmtBytes(traffic.totals.down)} · ↑ ${fmtBytes(traffic.totals.up)}` : " "} />
        <StatCard icon={Database} accent="info" label="累计流量" value={fmtBytes(d.traffic_total)} hint={isAdmin ? `自实例启动起 · ${d.users_total} 个用户` : "自实例启动起"} />
      </div>

      <div className="mt-4 grid gap-4 xl:grid-cols-3">
        <Card className="xl:col-span-2">
          <CardHeader className="flex-row items-start justify-between gap-4 space-y-0">
            <div className="space-y-1">
              <CardTitle>流量趋势</CardTitle>
              <CardDescription>近 24 小时 · 每小时汇总</CardDescription>
            </div>
            <ChartLegend />
          </CardHeader>
          <CardContent className="pl-2">
            {traffic ? <TrafficChart points={traffic.points} range="24h" height={260} /> : <Skeleton className="ml-3 h-[260px]" />}
          </CardContent>
        </Card>

        <Card className="flex flex-col">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">需要关注{attention.length > 0 && <Badge variant="destructive">{attention.length}</Badge>}</CardTitle>
            <CardDescription>异常转发、超配额与离线节点</CardDescription>
          </CardHeader>
          <CardContent className="flex-1">
            {!rules ? <div className="space-y-3"><Skeleton className="h-12" /><Skeleton className="h-12" /></div>
              : attention.length === 0 ? (
                <div className="flex h-full min-h-40 flex-col items-center justify-center gap-2 text-center">
                  <CheckCircle2 className="h-9 w-9 text-success" strokeWidth={1.6} />
                  <div className="text-sm font-medium">一切正常</div>
                  <div className="text-xs text-muted-foreground">没有需要处理的问题</div>
                </div>
              ) : (
                <ul className="-mx-2 max-h-[264px] space-y-1 overflow-y-auto">
                  {attention.map((a) => (
                    <li key={a.key}>
                      <button onClick={() => onNavigate(a.to)} className="group flex w-full items-start gap-3 rounded-lg p-2 text-left transition-colors hover:bg-accent">
                        <span className={cn("mt-0.5 grid h-7 w-7 shrink-0 place-items-center rounded-md", a.tone === "destructive" ? "bg-destructive/10 text-destructive" : "bg-warning/10 text-warning")}>
                          <a.icon className="h-3.5 w-3.5" />
                        </span>
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-[13px] font-medium">{a.title}</span>
                          <span className="block truncate text-xs text-muted-foreground">{a.desc}</span>
                        </span>
                        <ArrowRight className="mt-1.5 h-3.5 w-3.5 shrink-0 text-muted-foreground opacity-0 transition group-hover:opacity-100" />
                      </button>
                    </li>
                  ))}
                </ul>
              )}
          </CardContent>
        </Card>
      </div>

      <div className={cn("mt-4 grid gap-4 lg:grid-cols-2", m && "xl:grid-cols-3")}>
        {m && <Card>
          <CardHeader><CardTitle>主控主机</CardTitle><CardDescription className="truncate">{m.hostname || m.os || "本机"}</CardDescription></CardHeader>
          <CardContent className="space-y-4">
            <div>
              <div className="mb-1.5 flex items-center justify-between text-[13px]"><span className="flex items-center gap-2 text-muted-foreground"><HardDrive className="h-4 w-4" />内存</span><span className="tabular">{fmtBytes(memUsed)} / {fmtBytes(m.mem_total)}</span></div>
              <Meter value={memPct} />
            </div>
            <div className="flex items-center justify-between text-[13px]">
              <span className="flex items-center gap-2 text-muted-foreground"><Cpu className="h-4 w-4" />CPU · 负载</span>
              <span className="tabular">{m.cpu_cores || "-"} 核 <span className="text-muted-foreground">· {load.length ? load.join(" / ") : "-"}</span></span>
            </div>
            {t && (
              <div className="flex items-center justify-between text-[13px]">
                <span className="flex items-center gap-2 text-muted-foreground"><Gauge className="h-4 w-4" />拥塞控制</span>
                <span className="flex items-center gap-2"><span className="font-mono text-xs">{t.congestion_control || "-"}</span>
                  {t.bbr_active ? <Badge variant="success">BBR</Badge> : t.bbr_supported ? <Badge variant="warning">未启用 BBR</Badge> : <Badge variant="secondary">不支持 BBR</Badge>}
                </span>
              </div>
            )}
            <div className="grid grid-cols-2 gap-3 rounded-lg bg-muted/50 p-3 text-xs">
              <div><div className="text-muted-foreground">系统</div><div className="mt-0.5 truncate font-medium">{m.os || "-"}</div></div>
              <div><div className="text-muted-foreground">内核</div><div className="mt-0.5 truncate font-mono">{m.kernel || "-"}</div></div>
            </div>
          </CardContent>
        </Card>}

        <Card>
          <CardHeader className="flex-row items-start justify-between space-y-0">
            <div className="space-y-1"><CardTitle>流量排行</CardTitle><CardDescription>近 24 小时 Top 5 转发</CardDescription></div>
            <Button variant="ghost" size="xs" onClick={() => onNavigate("traffic")}>详情<ArrowRight /></Button>
          </CardHeader>
          <CardContent>
            {!traffic ? <div className="space-y-3"><Skeleton className="h-8" /><Skeleton className="h-8" /><Skeleton className="h-8" /></div>
              : topRules.length === 0 ? <div className="py-10 text-center text-[13px] text-muted-foreground">近 24 小时没有流量</div>
              : (
                <ol className="space-y-3.5">
                  {topRules.map((r, i) => (
                    <li key={r.tag}>
                      <div className="mb-1.5 flex items-center justify-between gap-3 text-[13px]">
                        <span className="flex min-w-0 items-center gap-2"><span className="tabular w-4 text-xs text-muted-foreground">{i + 1}</span><span className="truncate font-medium">{r.tag}</span></span>
                        <span className="tabular shrink-0 text-muted-foreground">{fmtBytes(r.up + r.down)}</span>
                      </div>
                      <div className="ml-6 h-1.5 overflow-hidden rounded-full bg-muted">
                        <div className="h-full rounded-full bg-gradient-to-r from-chart-1 to-chart-2" style={{ width: `${((r.up + r.down) / topMax) * 100}%` }} />
                      </div>
                    </li>
                  ))}
                </ol>
              )}
          </CardContent>
        </Card>

        {isAdmin ? (
          <Card className="lg:col-span-2 xl:col-span-1">
            <CardHeader className="flex-row items-start justify-between space-y-0">
              <div className="space-y-1"><CardTitle>节点状态</CardTitle><CardDescription>被控节点约每 5 秒上报一次</CardDescription></div>
              <Button variant="ghost" size="xs" onClick={() => onNavigate("nodes")}>管理<ArrowRight /></Button>
            </CardHeader>
            <CardContent>
              {!nodes ? <div className="space-y-3"><Skeleton className="h-10" /><Skeleton className="h-10" /></div>
                : nodes.length === 0 ? <div className="py-10 text-center text-[13px] text-muted-foreground">还没有被控节点</div>
                : (
                  <ul className="-mx-2 max-h-[220px] space-y-0.5 overflow-y-auto">
                    {nodes.map((n) => (
                      <li key={n.id} className="flex items-center gap-3 rounded-lg px-2 py-2 text-[13px] hover:bg-muted/50">
                        <StatusDot tone={nodeTone(n)} live={n.online && !n.deleting} />
                        <span className="min-w-0 flex-1 truncate font-medium">{n.name}</span>
                        <span className="text-xs text-muted-foreground">{n.rule_count} 条</span>
                        <span className="tabular w-20 text-right text-xs text-muted-foreground">{fmtRelative(n.last_seen)}</span>
                      </li>
                    ))}
                  </ul>
                )}
            </CardContent>
          </Card>
        ) : (
          <Card>
            <CardHeader><CardTitle>账户</CardTitle><CardDescription>你只能看到和管理自己创建的转发</CardDescription></CardHeader>
            <CardContent className="flex items-center gap-3 text-[13px] text-muted-foreground"><Users className="h-4 w-4" />如需更多节点或权限，请联系管理员</CardContent>
          </Card>
        )}
      </div>
    </div>
  );
}
