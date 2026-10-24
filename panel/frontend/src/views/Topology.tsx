import { useEffect, useMemo, useState } from "react";
import { api, type NodeInfo, type Rule } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";

// cc-switch 风格转发拓扑：客户端 → 中转节点（本机/被控） → 落地机
// 纯 SVG 绘制，节点卡片与连线状态着色（运行/停止/探测超时）

interface RemoteNode { key: string; host: string; port: number }

export function Topology() {
  const [rules, setRules] = useState<Rule[] | null>(null);

  useEffect(() => {
    api<{ rules: Rule[] }>("/api/rules").then((d) => setRules(d.rules || [])).catch(() => setRules([]));
  }, []);

  const layout = useMemo(() => {
    if (!rules) return null;
    // 按节点分组
    const groups = new Map<string, { id: number; name: string; online: boolean; rules: Rule[] }>();
    for (const r of rules) {
      const g = groups.get(r.node_name) || { id: r.node_id, name: r.node_name, online: r.status.active !== "node-offline", rules: [] };
      g.rules.push(r);
      groups.set(r.node_name, g);
    }
    // 落地去重
    const remotes = new Map<string, RemoteNode>();
    for (const r of rules) {
      const key = `${r.remote_host}:${r.remote_tcp_port}`;
      if (!remotes.has(key)) remotes.set(key, { key, host: r.remote_host, port: r.remote_tcp_port });
    }
    const nodeList = [...groups.values()];
    const remoteList = [...remotes.values()];
    return { nodeList, remoteList, ruleRemote: new Map(rules.map((r) => [r.tag, `${r.remote_host}:${r.remote_tcp_port}`])) };
  }, [rules]);

  if (!rules || !layout) return <div className="py-16 text-center text-sm text-muted-foreground">加载中…</div>;

  // 布局尺寸
  const nodeH = (g: any) => 56 + g.rules.length * 30;
  const totalNodeH = layout.nodeList.reduce((s, g) => s + nodeH(g) + 20, 0);
  const remoteH = 56;
  const totalRemoteH = layout.remoteList.length * (remoteH + 14) + 14;
  const H = Math.max(320, Math.max(totalNodeH, totalRemoteH) + 40);
  const colX = { client: 30, node: 300, remote: 760 };
  const W = 1120;

  // 计算每个节点卡片 y 与每条规则锚点 y
  let yCursor = 20;
  const nodePos = layout.nodeList.map((g) => {
    const y = yCursor;
    yCursor += nodeH(g) + 20;
    return { g, y, h: nodeH(g) };
  });
  let ryCursor = (H - totalRemoteH) / 2;
  const remotePos = layout.remoteList.map((r) => {
    const y = ryCursor;
    ryCursor += remoteH + 14;
    return { r, y };
  });

  const edgeColor = (r: Rule) => {
    if ((r.desired_state || "running") === "stopped") return "#64748b";
    if (r.probe && !r.probe.ok && !r.probe.soft) return "#ef4444";
    if (r.probe && !r.probe.ok) return "#f59e0b";
    return "#3b82f6";
  };
  const edgeDash = (r: Rule) => ((r.desired_state || "running") === "stopped" ? "6 5" : undefined);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-xl font-semibold tracking-tight">转发拓扑</h2>
          <p className="mt-1 text-xs text-muted-foreground">
            客户端 → 中转节点 → 落地机；连线颜色 = 运行(蓝) / 停止(灰) / 探测超时(红) / UDP 探测无响应(黄)
          </p>
        </div>
        <div className="flex gap-3 text-xs text-muted-foreground">
          <span className="flex items-center gap-1.5"><i className="h-2.5 w-2.5 rounded-full bg-blue-500" />运行</span>
          <span className="flex items-center gap-1.5"><i className="h-2.5 w-2.5 rounded-full bg-slate-500" />停止</span>
          <span className="flex items-center gap-1.5"><i className="h-2.5 w-2.5 rounded-full bg-red-500" />超时</span>
          <span className="flex items-center gap-1.5"><i className="h-2.5 w-2.5 rounded-full bg-amber-500" />UDP 无响应</span>
        </div>
      </div>

      {rules.length === 0 ? (
        <Card><CardContent className="py-14 text-center text-sm text-muted-foreground">还没有转发规则，先到「转发管理」创建</CardContent></Card>
      ) : (
        <Card className="overflow-x-auto">
          <svg width={W} height={H} className="min-w-[900px]">
            {/* 客户端 */}
            <rect x={colX.client} y={H / 2 - 28} width={170} height={56} rx={12} className="fill-secondary stroke-border" />
            <text x={colX.client + 85} y={H / 2 - 4} textAnchor="middle" className="fill-foreground text-[13px] font-medium">客户端</text>
            <text x={colX.client + 85} y={H / 2 + 14} textAnchor="middle" className="fill-muted-foreground text-[10.5px]">用户流量入口</text>

            {/* 节点卡片 */}
            {nodePos.map(({ g, y, h }) => {
              const midY = y + 28;
              return (
                <g key={g.name}>
                  <path d={`M ${colX.client + 170} ${H / 2} C ${colX.client + 240} ${H / 2}, ${colX.node - 60} ${midY}, ${colX.node} ${midY}`}
                    stroke="#475569" strokeWidth={1.6} fill="none" opacity={0.7} />
                  <rect x={colX.node} y={y} width={300} height={h} rx={12}
                    className={g.online || g.id === 0 ? "fill-card stroke-border" : "fill-card stroke-red-500/50"} />
                  <text x={colX.node + 16} y={y + 24} className="fill-foreground text-[13px] font-semibold">{g.name}</text>
                  {g.id !== 0 && (
                    <text x={colX.node + 16} y={y + 40} className="fill-muted-foreground text-[10.5px]">{g.online ? "节点在线" : "节点离线"}</text>
                  )}
                  {g.id === 0 && (
                    <text x={colX.node + 16} y={y + 40} className="fill-muted-foreground text-[10.5px]">主控服务器</text>
                  )}
                  {g.rules.map((r, i) => {
                    const ry = y + 58 + i * 30;
                    return (
                      <g key={r.tag}>
                        <text x={colX.node + 16} y={ry + 4} className="fill-muted-foreground font-mono text-[10.5px]">
                          {r.tag} :{r.listen_port}
                        </text>
                        {r.probe?.ok && (
                          <text x={colX.node + 284} y={ry + 4} textAnchor="end" className="fill-emerald-400 font-mono text-[10px]">{r.probe.ms}ms</text>
                        )}
                      </g>
                    );
                  })}
                </g>
              );
            })}

            {/* 落地卡片 */}
            {remotePos.map(({ r, y }) => {
              const midY = y + remoteH / 2;
              return (
                <g key={r.key}>
                  <rect x={colX.remote} y={y} width={300} height={remoteH} rx={12} className="fill-card stroke-border" />
                  <text x={colX.remote + 16} y={y + 24} className="fill-foreground font-mono text-[12px] font-medium">{r.host}</text>
                  <text x={colX.remote + 16} y={y + 42} className="fill-muted-foreground font-mono text-[10.5px]">:{r.port}</text>
                  {/* 规则 → 落地 连线 */}
                  {nodePos.map(({ g, y: ny, h: nh }) =>
                    g.rules.filter((rule) => layout.ruleRemote.get(rule.tag) === r.key).map((rule, i) => {
                      const sy = ny + 58 + g.rules.indexOf(rule) * 30;
                      return (
                        <path key={rule.tag}
                          d={`M ${colX.node + 300} ${sy} C ${colX.node + 380} ${sy}, ${colX.remote - 70} ${midY}, ${colX.remote} ${midY}`}
                          stroke={edgeColor(rule)} strokeWidth={1.8} strokeDasharray={edgeDash(rule)} fill="none" opacity={0.85} />
                      );
                    })
                  )}
                </g>
              );
            })}

            {/* 图例注释 */}
            <text x={colX.remote + 310} y={24} className="fill-muted-foreground text-[10px]">按 tag:监听端口 出边，落地按 host:port 去重</text>
          </svg>
        </Card>
      )}

      {/* 汇总表 */}
      <Card className="overflow-hidden pb-0">
        <div className="p-4 pb-2 text-sm font-semibold">链路明细</div>
        <table className="w-full text-sm">
          <thead><tr className="border-b text-left text-xs text-muted-foreground">
            <th className="px-4 py-2.5 font-medium">转发</th><th className="px-4 py-2.5 font-medium">节点</th>
            <th className="px-4 py-2.5 font-medium">链路</th><th className="px-4 py-2.5 font-medium">探测</th><th className="px-4 py-2.5 font-medium">状态</th>
          </tr></thead>
          <tbody>
            {rules.map((r) => (
              <tr key={r.tag} className="border-b last:border-0 hover:bg-muted/40">
                <td className="px-4 py-2.5 font-medium">{r.tag}</td>
                <td className="px-4 py-2.5">{r.node_id === 0 ? <Badge variant="secondary">本机</Badge> : <Badge variant="info">{r.node_name}</Badge>}</td>
                <td className="px-4 py-2.5 font-mono text-xs">:{r.listen_port} → {r.remote_host}:{r.remote_tcp_port}</td>
                <td className="px-4 py-2.5 font-mono text-xs">
                  {r.probe ? (r.probe.ok ? <span className="text-emerald-400">{r.probe.ms}ms</span> : <span className={r.probe.soft ? "text-amber-400" : "text-red-400"}>失败</span>) : <span className="text-muted-foreground">-</span>}
                </td>
                <td className="px-4 py-2.5">{(r.desired_state || "running") === "running" ? <Badge variant="success">运行</Badge> : <Badge variant="secondary">停止</Badge>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>
    </div>
  );
}
