import { useCallback, useMemo, useState } from "react";
import { ArrowDownToLine, ArrowUpFromLine, BarChart3, Database, PlugZap } from "lucide-react";
import { api, type NodeInfo, type TrafficData } from "@/api";
import { cn, fmtBytes, fmtNumber } from "@/lib/utils";
import { usePolling } from "@/lib/usePolling";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/misc";
import { Tooltip } from "@/components/ui/tooltip";
import { EmptyState, ErrorBanner, PageHeader, StatCard } from "@/components/common";
import { ChartLegend, TrafficChart } from "@/components/traffic-chart";

const RANGES: [string, string, string][] = [
  ["24h", "24 小时", "每小时"], ["7d", "7 天", "每天"], ["30d", "30 天", "每天"], ["365d", "一年", "每周"],
];

const dayKey = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
const LEVELS = ["bg-muted", "bg-primary/25", "bg-primary/45", "bg-primary/70", "bg-primary"];

// 按分位数分级：个别极端日不会把其它日期都压成最浅色
function level(v: number, q: number[]) {
  if (v <= 0) return 0;
  return v <= q[0] ? 1 : v <= q[1] ? 2 : v <= q[2] ? 3 : 4;
}

function Heatmap({ days }: { days: { date: string; up: number; down: number }[] }) {
  const { weeks, months, quartiles, total, activeDays } = useMemo(() => {
    const map = new Map(days.map((d) => [d.date, d.up + d.down]));
    const today = new Date(); today.setHours(0, 0, 0, 0);
    const end = new Date(today); end.setDate(end.getDate() + (6 - end.getDay()));
    const start = new Date(end); start.setDate(start.getDate() - 53 * 7 + 1);
    const vals = [...map.values()].filter((v) => v > 0).sort((a, b) => a - b);
    const at = (p: number) => vals[Math.min(vals.length - 1, Math.floor(vals.length * p))] || 0;
    const weeks: { key: string; v: number; future: boolean }[][] = [];
    const months: { col: number; label: string }[] = [];
    let lastMonth = -1;
    for (let w = 0; w < 53; w++) {
      const col = [];
      for (let dow = 0; dow < 7; dow++) {
        const day = new Date(start); day.setDate(start.getDate() + w * 7 + dow);
        const key = dayKey(day);
        if (dow === 0 && day.getMonth() !== lastMonth) {
          lastMonth = day.getMonth();
          // 起始列只露出几天的月份不标注，避免与下个月的标签重叠
          if (months.length && w - months[months.length - 1].col < 3) months.pop();
          months.push({ col: w, label: `${day.getMonth() + 1}月` });
        }
        col.push({ key, v: map.get(key) || 0, future: day > today });
      }
      weeks.push(col);
    }
    return { weeks, months, quartiles: [at(0.25), at(0.5), at(0.75)], total: vals.reduce((a, b) => a + b, 0), activeDays: vals.length };
  }, [days]);

  return (
    <div>
      <div className="overflow-x-auto pb-2">
        <div className="inline-grid gap-y-1.5" style={{ gridTemplateColumns: "auto 1fr" }}>
          <div />
          <div className="relative h-4 text-[11px] text-muted-foreground">
            {months.map((m) => <span key={m.col} className="absolute" style={{ left: m.col * 15 }}>{m.label}</span>)}
          </div>
          <div className="grid grid-rows-7 gap-[3px] pr-2 text-[10px] leading-3 text-muted-foreground">
            {["", "一", "", "三", "", "五", ""].map((d, i) => <span key={i} className="h-3">{d}</span>)}
          </div>
          <div className="flex gap-[3px]">
            {weeks.map((col, w) => (
              <div key={w} className="grid grid-rows-7 gap-[3px]">
                {col.map((c) => c.future ? <div key={c.key} className="h-3 w-3" /> : (
                  <Tooltip key={c.key} content={`${c.key} · ${fmtBytes(c.v)}`}>
                    <div className={cn("h-3 w-3 rounded-[3px] ring-1 ring-inset ring-black/[0.04] transition-transform hover:scale-125 dark:ring-white/[0.04]", LEVELS[level(c.v, quartiles)])} />
                  </Tooltip>
                ))}
              </div>
            ))}
          </div>
        </div>
      </div>
      <div className="mt-3 flex flex-wrap items-center justify-between gap-3 text-xs text-muted-foreground">
        <span>过去一年共 <b className="tabular font-medium text-foreground">{fmtBytes(total)}</b>，有流量 {activeDays} 天</span>
        <span className="flex items-center gap-1.5">少{LEVELS.map((c) => <i key={c} className={cn("h-3 w-3 rounded-[3px]", c)} />)}多</span>
      </div>
    </div>
  );
}

export function Traffic() {
  const [range, setRange] = useState("24h");
  const [groupMode, setGroupMode] = useState<"rule" | "node">("rule");

  const request = useCallback((signal: AbortSignal) => api<TrafficData>(`/api/traffic?range=${range}`, { signal }), [range]);
  const nodesReq = useCallback((signal: AbortSignal) => api<{ nodes: NodeInfo[] }>("/api/nodes", { signal }).then((d) => d.nodes || []), []);
  const { data, error } = usePolling(request, 30000);
  const { data: nodes } = usePolling(nodesReq, 60000);
  const d = data && data.range === range ? data : null;
  const nodeName = useCallback((id: number) => (id === 0 ? "本机" : nodes?.find((n) => n.id === id)?.name || `节点 #${id}`), [nodes]);

  const groups = useMemo(() => {
    if (!d) return [];
    if (groupMode === "rule") return [...d.rows].sort((a, b) => b.up + b.down - (a.up + a.down)).map((r) => ({ ...r, name: r.tag, sub: nodeName(r.node_id) }));
    const m = new Map<number, { name: string; sub: string; node_id: number; up: number; down: number; conns: number; count: number }>();
    for (const r of d.rows) {
      const g = m.get(r.node_id) || { name: nodeName(r.node_id), sub: "", node_id: r.node_id, up: 0, down: 0, conns: 0, count: 0 };
      g.up += r.up; g.down += r.down; g.conns += r.conns; g.count++;
      m.set(r.node_id, g);
    }
    return [...m.values()].map((g) => ({ ...g, sub: `${g.count} 条转发` })).sort((a, b) => b.up + b.down - (a.up + a.down));
  }, [d, groupMode, nodeName]);
  const grand = groups.reduce((s, g) => s + g.up + g.down, 0) || 1;
  const rangeInfo = RANGES.find(([k]) => k === range)!;

  return (
    <div>
      <PageHeader
        title="流量统计"
        description="基于转发内核计数每分钟采样，按时间范围聚合 TCP 与 UDP 用量，日汇总保留 400 天。"
        actions={
          <Tabs value={range} onValueChange={setRange}>
            <TabsList>{RANGES.map(([k, label]) => <TabsTrigger key={k} value={k}>{label}</TabsTrigger>)}</TabsList>
          </Tabs>
        }
      />
      <ErrorBanner message={error} />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard icon={ArrowDownToLine} label="下载" hint="远端 → 客户端" value={d ? fmtBytes(d.totals.down) : <Skeleton className="h-8 w-24" />} />
        <StatCard icon={ArrowUpFromLine} accent="chart-2" label="上传" hint="客户端 → 远端" value={d ? fmtBytes(d.totals.up) : <Skeleton className="h-8 w-24" />} />
        <StatCard icon={Database} accent="info" label="合计" hint={`近 ${rangeInfo[1]}`} value={d ? fmtBytes(d.totals.up + d.totals.down) : <Skeleton className="h-8 w-24" />} />
        <StatCard icon={PlugZap} accent="warning" label="新建连接" hint="TCP 连接 + UDP 会话" value={d ? fmtNumber(d.totals.conns) : <Skeleton className="h-8 w-24" />} />
      </div>

      <Card className="mt-4">
        <CardHeader className="flex-row flex-wrap items-start justify-between gap-4 space-y-0">
          <div className="space-y-1"><CardTitle>流量趋势</CardTitle><CardDescription>近 {rangeInfo[1]} · {rangeInfo[2]}汇总</CardDescription></div>
          <ChartLegend />
        </CardHeader>
        <CardContent className="pl-2">{d ? <TrafficChart points={d.points} range={range} height={300} /> : <Skeleton className="ml-3 h-[300px]" />}</CardContent>
      </Card>

      <div className="mt-4 grid gap-4 2xl:grid-cols-[auto_1fr]">
        <Card className="min-w-0">
          <CardHeader><CardTitle>年度热力图</CardTitle><CardDescription>每日上传 + 下载合计</CardDescription></CardHeader>
          <CardContent>{d ? <Heatmap days={d.heatmap} /> : <Skeleton className="h-32" />}</CardContent>
        </Card>

        <Card className="min-w-0 overflow-hidden">
          <CardHeader className="flex-row flex-wrap items-start justify-between gap-3 space-y-0">
            <div className="space-y-1"><CardTitle>分组统计</CardTitle><CardDescription>近 {rangeInfo[1]}，按用量排序</CardDescription></div>
            <Tabs value={groupMode} onValueChange={(v) => setGroupMode(v as "rule" | "node")}>
              <TabsList><TabsTrigger value="rule">按转发</TabsTrigger><TabsTrigger value="node">按节点</TabsTrigger></TabsList>
            </Tabs>
          </CardHeader>
          {!d ? <CardContent><Skeleton className="h-40" /></CardContent> : groups.length === 0 ? (
            <EmptyState icon={BarChart3} title="本期没有流量数据" description="转发产生流量后，约 1 分钟内出现在这里" />
          ) : (
            <Table>
              <TableHeader><TableRow className="hover:bg-transparent">
                <TableHead>{groupMode === "rule" ? "转发" : "节点"}</TableHead>
                <TableHead className="text-right">下载</TableHead>
                <TableHead className="text-right">上传</TableHead>
                <TableHead className="text-right">合计</TableHead>
                <TableHead className="w-48">占比</TableHead>
                <TableHead className="text-right">连接</TableHead>
              </TableRow></TableHeader>
              <TableBody>
                {groups.map((g) => {
                  const v = g.up + g.down;
                  const pct = (v / grand) * 100;
                  return (
                    <TableRow key={g.name + g.node_id}>
                      <TableCell><div className="font-medium">{g.name}</div><div className="text-xs text-muted-foreground">{g.sub}</div></TableCell>
                      <TableCell className="tabular text-right font-mono text-xs">{fmtBytes(g.down)}</TableCell>
                      <TableCell className="tabular text-right font-mono text-xs">{fmtBytes(g.up)}</TableCell>
                      <TableCell className="tabular text-right font-mono text-xs font-semibold">{fmtBytes(v)}</TableCell>
                      <TableCell>
                        <div className="flex items-center gap-2.5">
                          <div className="flex h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
                            <div className="h-full bg-chart-1" style={{ width: `${(g.down / grand) * 100}%` }} />
                            <div className="h-full bg-chart-2" style={{ width: `${(g.up / grand) * 100}%` }} />
                          </div>
                          <span className="tabular w-11 text-right text-xs text-muted-foreground">{pct.toFixed(1)}%</span>
                        </div>
                      </TableCell>
                      <TableCell className="tabular text-right font-mono text-xs">{fmtNumber(g.conns)}</TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </Card>
      </div>
    </div>
  );
}
