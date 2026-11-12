import { useCallback, useMemo, useState } from "react";
import { Waypoints } from "lucide-react";
import { api, type Rule } from "@/api";
import { cn } from "@/lib/utils";
import { usePolling } from "@/lib/usePolling";
import { ruleDestinations } from "@/lib/ruleDestinations";
import { probeView, ruleState, toneText } from "@/lib/ruleState";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { EmptyState, ErrorBanner, LoadingState, PageHeader, StatusLabel } from "@/components/common";

// 客户端 → 中转节点（本机/被控） → 落地机；悬停规则或落地高亮对应链路

interface Group { id: number; name: string; online: boolean; rules: Rule[] }
interface Remote { key: string; host: string; port: number; protocols: string[] }

const COL = { client: 0, clientW: 132, node: 220, nodeW: 280, remote: 610, remoteW: 250 };
const W = COL.remote + COL.remoteW + 2;
const NODE_HEAD = 50, ROW = 30, NODE_GAP = 18, REMOTE_H = 52, REMOTE_GAP = 12;

type EdgeKind = "run" | "stop" | "fail" | "soft";
const EDGE: Record<EdgeKind, { color: string; label: string; dash?: string }> = {
  run: { color: "hsl(var(--primary))", label: "运行" },
  stop: { color: "hsl(var(--muted-foreground))", label: "停止", dash: "5 5" },
  fail: { color: "hsl(var(--destructive))", label: "探测超时" },
  soft: { color: "hsl(var(--warning))", label: "UDP 无响应" },
};

function edgeKind(r: Rule): EdgeKind {
  if ((r.desired_state || "running") === "stopped") return "stop";
  if (r.probe && !r.probe.ok) return r.probe.soft ? "soft" : "fail";
  return "run";
}

const curve = (x1: number, y1: number, x2: number, y2: number) => {
  const dx = (x2 - x1) * 0.5;
  return `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`;
};

export function Topology() {
  const request = useCallback((signal: AbortSignal) => api<{ rules: Rule[] }>("/api/rules", { signal }), []);
  const { data, error } = usePolling(request);
  const rules = data?.rules;
  const [hover, setHover] = useState<{ tag?: string; remote?: string; node?: number } | null>(null);

  const layout = useMemo(() => {
    if (!rules) return null;
    const groups = new Map<number, Group>();
    for (const r of rules) {
      const g = groups.get(r.node_id) || { id: r.node_id, name: r.node_name, online: r.status.active !== "node-offline", rules: [] };
      g.rules.push(r);
      groups.set(r.node_id, g);
    }
    const nodeList = [...groups.values()].sort((a, b) => a.id - b.id);
    const remotes = new Map<string, Remote>();
    const ruleRemotes = new Map<string, string[]>();
    for (const r of rules) {
      const dests = ruleDestinations(r);
      ruleRemotes.set(r.tag, dests.map((d) => d.key));
      for (const d of dests) {
        const ex = remotes.get(d.key);
        if (ex) ex.protocols = [...new Set([...ex.protocols, ...d.protocols])];
        else remotes.set(d.key, { ...d });
      }
    }
    const remoteList = [...remotes.values()];

    const nodeH = (g: Group) => NODE_HEAD + g.rules.length * ROW + 8;
    const totalNodeH = nodeList.reduce((s, g) => s + nodeH(g) + NODE_GAP, -NODE_GAP);
    const totalRemoteH = remoteList.length * (REMOTE_H + REMOTE_GAP) - REMOTE_GAP;
    const H = Math.max(260, totalNodeH, totalRemoteH) + 24;

    let y = (H - totalNodeH) / 2;
    const nodePos = nodeList.map((g) => { const p = { g, y, h: nodeH(g) }; y += p.h + NODE_GAP; return p; });
    let ry = (H - totalRemoteH) / 2;
    const remotePos = new Map(remoteList.map((r) => { const p = { r, y: ry }; ry += REMOTE_H + REMOTE_GAP; return [r.key, p] as const; }));
    return { nodePos, remotePos, ruleRemotes, H };
  }, [rules]);

  if (!rules || !layout) return <LoadingState error={error} />;
  const { nodePos, remotePos, ruleRemotes, H } = layout;
  const clientY = H / 2;

  // 当前悬停涉及的规则集合
  const active = (r: Rule) => {
    if (!hover) return true;
    if (hover.tag) return hover.tag === r.tag;
    if (hover.remote) return ruleRemotes.get(r.tag)?.includes(hover.remote) ?? false;
    if (hover.node !== undefined) return r.node_id === hover.node;
    return true;
  };
  const remoteActive = (key: string) => !hover || rules.some((r) => active(r) && ruleRemotes.get(r.tag)?.includes(key));

  return (
    <div>
      <PageHeader title="转发拓扑" description="客户端 → 中转节点 → 落地机。悬停规则、节点或落地机可高亮对应链路，落地按 host:port 去重。" />
      <ErrorBanner message={error} />

      {rules.length === 0 ? (
        <Card><EmptyState icon={Waypoints} title="还没有转发规则" description="先到「转发规则」创建转发，这里会自动绘制链路图。" /></Card>
      ) : (
        <Card className="overflow-hidden">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b px-5 py-3">
            <div className="flex items-center gap-4 text-xs text-muted-foreground">
              {(Object.keys(EDGE) as EdgeKind[]).map((k) => (
                <span key={k} className="flex items-center gap-1.5">
                  <svg width="18" height="6"><line x1="0" y1="3" x2="18" y2="3" stroke={EDGE[k].color} strokeWidth="2" strokeDasharray={EDGE[k].dash} /></svg>
                  {EDGE[k].label}
                </span>
              ))}
            </div>
            <span className="text-xs text-muted-foreground">{rules.length} 条转发 · {nodePos.length} 个中转 · {remotePos.size} 个落地</span>
          </div>
          <div className="bg-grid overflow-x-auto bg-muted/20 p-6 [background-size:24px_24px]">
            <svg viewBox={`-4 0 ${W + 8} ${H}`} className="mx-auto w-full min-w-[820px] max-w-[1100px] select-none" style={{ height: "auto" }} onMouseLeave={() => setHover(null)}>
              {/* 边：客户端 → 节点 */}
              {nodePos.map(({ g, y }) => (
                <path key={`c-${g.id}`} d={curve(COL.clientW, clientY, COL.node, y + NODE_HEAD / 2)} fill="none"
                  stroke="hsl(var(--muted-foreground))" strokeWidth={1.5} strokeOpacity={!hover || g.rules.some(active) ? 0.45 : 0.08} className="transition-[stroke-opacity]" />
              ))}
              {/* 边：规则 → 落地 */}
              {nodePos.flatMap(({ g, y }) => g.rules.flatMap((r, i) => {
                const sy = y + NODE_HEAD + i * ROW + ROW / 2;
                const k = edgeKind(r);
                const on = active(r);
                return (ruleRemotes.get(r.tag) || []).map((key) => {
                  const rp = remotePos.get(key)!;
                  const d = curve(COL.node + COL.nodeW, sy, COL.remote, rp.y + REMOTE_H / 2);
                  return (
                    <g key={`${r.tag}-${key}`} className="transition-opacity" opacity={on ? 1 : 0.08}>
                      <path d={d} fill="none" stroke={EDGE[k].color} strokeWidth={hover && on ? 2.4 : 1.6} strokeDasharray={EDGE[k].dash} strokeOpacity={0.85} />
                      {k === "run" && on && <path d={d} fill="none" stroke={EDGE[k].color} strokeWidth={2} strokeDasharray="2 18" strokeLinecap="round" className="animate-flow" />}
                    </g>
                  );
                });
              }))}

              {/* 客户端 */}
              <g>
                <rect x={COL.client} y={clientY - 30} width={COL.clientW} height={60} rx={14} className="fill-card stroke-border" strokeWidth={1} />
                <circle cx={COL.client + 26} cy={clientY} r={11} className="fill-primary/10" />
                <circle cx={COL.client + 26} cy={clientY} r={4} className="fill-primary" />
                <text x={COL.client + 46} y={clientY - 3} className="fill-foreground text-[13px] font-semibold">客户端</text>
                <text x={COL.client + 46} y={clientY + 14} className="fill-muted-foreground text-[11px]">用户流量入口</text>
                <circle cx={COL.clientW} cy={clientY} r={3.5} className="fill-card stroke-muted-foreground" strokeWidth={1.5} />
              </g>

              {/* 中转节点 */}
              {nodePos.map(({ g, y, h }) => {
                const on = !hover || g.rules.some(active);
                const offline = g.id !== 0 && !g.online;
                return (
                  <g key={g.id} opacity={on ? 1 : 0.35} className="transition-opacity" onMouseEnter={() => setHover({ node: g.id })}>
                    <rect x={COL.node} y={y} width={COL.nodeW} height={h} rx={14} className={cn("fill-card", offline ? "stroke-destructive/60" : "stroke-border")} strokeWidth={1} />
                    <rect x={COL.node} y={y} width={COL.nodeW} height={NODE_HEAD - 6} rx={14} className="fill-muted/50" />
                    <circle cx={COL.node + 18} cy={y + 22} r={4} className={g.id === 0 ? "fill-primary" : offline ? "fill-destructive" : "fill-success"} />
                    <text x={COL.node + 30} y={y + 26} className="fill-foreground text-[13px] font-semibold">{g.name}</text>
                    <text x={COL.node + COL.nodeW - 14} y={y + 26} textAnchor="end" className="fill-muted-foreground text-[11px]">
                      {g.id === 0 ? "主控" : offline ? "离线" : "在线"} · {g.rules.length}
                    </text>
                    <circle cx={COL.node} cy={y + NODE_HEAD / 2} r={3.5} className="fill-card stroke-muted-foreground" strokeWidth={1.5} />
                    {g.rules.map((r, i) => {
                      const ry = y + NODE_HEAD + i * ROW;
                      const pv = probeView(r);
                      const k = edgeKind(r);
                      const isHover = hover?.tag === r.tag;
                      return (
                        <g key={r.tag} onMouseEnter={(e) => { e.stopPropagation(); setHover({ tag: r.tag }); }} onMouseLeave={() => setHover({ node: g.id })} className="cursor-default">
                          <rect x={COL.node + 6} y={ry + 2} width={COL.nodeW - 12} height={ROW - 4} rx={7} className={isHover ? "fill-accent" : "fill-transparent"} />
                          <text x={COL.node + 18} y={ry + ROW / 2 + 4} className="fill-foreground font-mono text-[11.5px] font-medium">{r.tag}</text>
                          <text x={COL.node + 132} y={ry + ROW / 2 + 4} className="fill-muted-foreground font-mono text-[11px]">:{r.listen_port}</text>
                          {pv && <text x={COL.node + COL.nodeW - 18} y={ry + ROW / 2 + 4} textAnchor="end" className={cn("font-mono text-[10.5px]", pv.tone === "success" ? "fill-success" : pv.tone === "warning" ? "fill-warning" : "fill-destructive")}>{pv.label}</text>}
                          <circle cx={COL.node + COL.nodeW} cy={ry + ROW / 2} r={3.5} fill={EDGE[k].color} className="stroke-card" strokeWidth={1.5} />
                        </g>
                      );
                    })}
                  </g>
                );
              })}

              {/* 落地机 */}
              {[...remotePos.values()].map(({ r, y }) => {
                const on = remoteActive(r.key);
                return (
                  <g key={r.key} opacity={on ? 1 : 0.35} className="transition-opacity" onMouseEnter={() => setHover({ remote: r.key })}>
                    <rect x={COL.remote} y={y} width={COL.remoteW} height={REMOTE_H} rx={12} className="fill-card stroke-border" strokeWidth={1} />
                    <text x={COL.remote + 16} y={y + 22} className="fill-foreground font-mono text-[12px] font-medium">{r.host.length > 26 ? r.host.slice(0, 25) + "…" : r.host}</text>
                    <text x={COL.remote + 16} y={y + 39} className="fill-muted-foreground font-mono text-[11px]">:{r.port} · {r.protocols.join("+")}</text>
                    <circle cx={COL.remote} cy={y + REMOTE_H / 2} r={3.5} className="fill-card stroke-muted-foreground" strokeWidth={1.5} />
                  </g>
                );
              })}
            </svg>
          </div>
        </Card>
      )}

      {rules.length > 0 && (
        <Card className="mt-4 overflow-hidden">
          <CardHeader><CardTitle>链路明细</CardTitle><CardDescription>每条转发的入口、落地与探测结果</CardDescription></CardHeader>
          <Table>
            <TableHeader><TableRow className="hover:bg-transparent">
              <TableHead>转发</TableHead><TableHead>中转</TableHead><TableHead>链路</TableHead><TableHead>探测</TableHead><TableHead>状态</TableHead>
            </TableRow></TableHeader>
            <TableBody>
              {rules.map((r) => {
                const pv = probeView(r);
                const st = ruleState(r);
                return (
                  <TableRow key={r.tag} onMouseEnter={() => setHover({ tag: r.tag })} onMouseLeave={() => setHover(null)} className={cn(hover?.tag === r.tag && "bg-muted/50")}>
                    <TableCell className="font-medium">{r.tag}</TableCell>
                    <TableCell className="text-[13px]">{r.node_id === 0 ? <Badge variant="default">本机</Badge> : r.node_name}</TableCell>
                    <TableCell className="font-mono text-xs">
                      {ruleDestinations(r).map((d) => (
                        <div key={d.key}><span className="text-muted-foreground">{d.protocols.join("+")}</span> :{r.listen_port} → {d.host}:{d.port}</div>
                      ))}
                    </TableCell>
                    <TableCell>{pv ? <span className={cn("font-mono text-xs", toneText[pv.tone])}>{pv.label}</span> : <span className="text-xs text-muted-foreground">—</span>}</TableCell>
                    <TableCell><StatusLabel tone={st.tone} live={st.live}>{st.label}</StatusLabel></TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </Card>
      )}
    </div>
  );
}
