import { useEffect, useState } from "react";
import { api, type TrafficData } from "@/api";
import { fmtBytes } from "@/lib/utils";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { TrafficBar } from "@/components/ui/progress";
import { ArrowDownToLine, ArrowUpFromLine, Database, PlugZap } from "lucide-react";
import {
  ResponsiveContainer, AreaChart, Area, XAxis, YAxis, CartesianGrid, Tooltip,
} from "recharts";

const RANGES: [string, string][] = [["24h", "今天"], ["7d", "近 7 天"], ["30d", "近 30 天"], ["365d", "全年"]];

function heatColor(v: number, max: number): string {
  if (v <= 0) return "hsl(240 3.7% 15.9%)";
  const r = v / max;
  if (r < 0.25) return "rgba(59,130,246,.28)";
  if (r < 0.5) return "rgba(59,130,246,.5)";
  if (r < 0.75) return "rgba(59,130,246,.72)";
  return "rgb(59,130,246)";
}

function Heatmap({ days }: { days: { date: string; up: number; down: number }[] }) {
  const map = new Map(days.map((d) => [d.date, d.up + d.down]));
  const today = new Date(); today.setHours(0, 0, 0, 0);
  const end = new Date(today); end.setDate(end.getDate() + (6 - end.getDay()));
  const start = new Date(end); start.setDate(start.getDate() - 52 * 7);
  const weeks = Math.ceil((+end - +start) / 7 / 86400000);
  const vals = [...map.values()].filter((v) => v > 0);
  const max = Math.max(...vals, 1);
  const monthLabels: { left: number; label: string }[] = [];
  let lastMonth = -1;
  const cells: JSX.Element[] = [];

  for (let w = 0; w < weeks; w++) {
    const col: JSX.Element[] = [];
    for (let dow = 0; dow < 7; dow++) {
      const day = new Date(start); day.setDate(day.getDate() + w * 7 + dow);
      if (day > today) { col.push(<div key={dow} className="h-[11px] w-[11px] rounded-[3px]" />); continue; }
      const key = `${day.getFullYear()}-${String(day.getMonth() + 1).padStart(2, "0")}-${String(day.getDate()).padStart(2, "0")}`;
      const v = map.get(key) || 0;
      if (dow === 0 && day.getMonth() !== lastMonth) {
        lastMonth = day.getMonth();
        monthLabels.push({ left: w * 14, label: `${day.getMonth() + 1}月` });
      }
      col.push(<div key={dow} title={`${key} · ${fmtBytes(v)}`} className="h-[11px] w-[11px] rounded-[3px]" style={{ background: heatColor(v, max) }} />);
    }
    cells.push(<div key={w} className="flex flex-col gap-[3px]">{col}</div>);
  }
  return (
    <div className="overflow-x-auto pb-1">
      <div className="relative mb-1.5 h-4" style={{ minWidth: weeks * 14 }}>
        {monthLabels.map((m, i) => (
          <span key={i} className="absolute text-[10.5px] text-muted-foreground" style={{ left: m.left }}>{m.label}</span>
        ))}
      </div>
      <div className="flex gap-[3px]">{cells}</div>
      <div className="mt-2 flex items-center gap-1.5 text-[11px] text-muted-foreground">
        少
        {[0, 0.15, 0.4, 0.65, 1].map((r) => (
          <div key={r} className="h-[11px] w-[11px] rounded-[3px]" style={{ background: r === 0 ? heatColor(0, 1) : heatColor(r * 100, 100) }} />
        ))}
        多
      </div>
    </div>
  );
}

export function Traffic() {
  const [range, setRange] = useState("24h");
  const [d, setD] = useState<TrafficData | null>(null);
  const [groupMode, setGroupMode] = useState<"rule" | "node">("rule");

  useEffect(() => {
    api<TrafficData>(`/api/traffic?range=${range}`).then(setD).catch(() => {});
  }, [range]);

  if (!d) return <div className="py-16 text-center text-sm text-muted-foreground">加载中…</div>;
  const totals = d.totals;

  let groups: { tag: string; node_id: number; up: number; down: number; conns: number }[] = [];
  if (groupMode === "node") {
    const m = new Map<number, typeof groups[number]>();
    for (const r of d.rows) {
      const g = m.get(r.node_id) || { tag: r.node_id === 0 ? "本机" : `节点 #${r.node_id}`, node_id: r.node_id, up: 0, down: 0, conns: 0 };
      g.up += r.up; g.down += r.down; g.conns += r.conns;
      m.set(r.node_id, g);
    }
    groups = [...m.values()].sort((a, b) => b.up + b.down - (a.up + a.down));
  } else {
    groups = d.rows;
  }
  const grand = groups.reduce((s, g) => s + g.up + g.down, 0) || 1;

  const chartData = d.points.map((p) => ({
    t: p.t * 1000,
    down: p.down,
    total: p.up + p.down,
    upText: fmtBytes(p.up),
    downText: fmtBytes(p.down),
  }));
  const fmtTime = (v: number) => {
    const t = new Date(v);
    return range === "24h" ? `${t.getHours()}:00` : range === "7d" ? `${t.getMonth() + 1}/${t.getDate()} ${t.getHours()}时` : `${t.getMonth() + 1}/${t.getDate()}`;
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-xl font-semibold tracking-tight">流量统计</h2>
          <p className="mt-1 text-xs text-muted-foreground">基于转发内核计数采样（每分钟），5 分钟粒度聚合，保留 48h 明细 + 400 天日汇总</p>
        </div>
        <Tabs value={range} onValueChange={setRange}>
          <TabsList>
            {RANGES.map(([k, label]) => <TabsTrigger key={k} value={k}>{label}</TabsTrigger>)}
          </TabsList>
        </Tabs>
      </div>

      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Card><CardContent className="flex items-center gap-3 p-4">
          <ArrowDownToLine className="h-8 w-8 text-blue-500" />
          <div><div className="text-xs text-muted-foreground">下载（远端→客户端）</div><div className="text-lg font-semibold">{fmtBytes(totals.down)}</div></div>
        </CardContent></Card>
        <Card><CardContent className="flex items-center gap-3 p-4">
          <ArrowUpFromLine className="h-8 w-8 text-emerald-500" />
          <div><div className="text-xs text-muted-foreground">上传（客户端→远端）</div><div className="text-lg font-semibold">{fmtBytes(totals.up)}</div></div>
        </CardContent></Card>
        <Card><CardContent className="flex items-center gap-3 p-4">
          <Database className="h-8 w-8 text-violet-500" />
          <div><div className="text-xs text-muted-foreground">合计流量</div><div className="text-lg font-semibold">{fmtBytes(totals.up + totals.down)}</div></div>
        </CardContent></Card>
        <Card><CardContent className="flex items-center gap-3 p-4">
          <PlugZap className="h-8 w-8 text-amber-500" />
          <div><div className="text-xs text-muted-foreground">新建连接</div><div className="text-lg font-semibold">{totals.conns} <span className="text-xs font-normal text-muted-foreground">次</span></div></div>
        </CardContent></Card>
      </div>

      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-sm">流量趋势</CardTitle>
          <div className="flex gap-4 text-xs text-muted-foreground">
            <span className="flex items-center gap-1.5"><i className="h-2.5 w-2.5 rounded-sm bg-blue-500" />下载</span>
            <span className="flex items-center gap-1.5"><i className="h-2.5 w-2.5 rounded-sm bg-emerald-500" />上传</span>
          </div>
        </CardHeader>
        <CardContent>
          <div className="h-[260px]">
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={chartData} margin={{ top: 8, right: 8, left: 52, bottom: 0 }}>
                <defs>
                  <linearGradient id="gDown" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="#3b82f6" stopOpacity={0.35} />
                    <stop offset="100%" stopColor="#3b82f6" stopOpacity={0} />
                  </linearGradient>
                  <linearGradient id="gTotal" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="#22c55e" stopOpacity={0.22} />
                    <stop offset="100%" stopColor="#22c55e" stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" stroke="rgba(139,151,179,.15)" />
                <XAxis dataKey="t" tickFormatter={fmtTime} tick={{ fill: "#8b97b3", fontSize: 11 }} axisLine={false} tickLine={false} minTickGap={40} />
                <YAxis tickFormatter={(v) => fmtBytes(v).replace(" ", "")} tick={{ fill: "#8b97b3", fontSize: 11 }} axisLine={false} tickLine={false} width={50} />
                <Tooltip
                  contentStyle={{ background: "hsl(240 10% 5.9%)", border: "1px solid hsl(240 3.7% 15.9%)", borderRadius: 8, fontSize: 12 }}
                  labelFormatter={(v) => fmtTime(Number(v))}
                  formatter={(value: any, name: any) => [fmtBytes(Number(value)), name === "total" ? "上传+下载" : "下载"]}
                />
                <Area type="monotone" dataKey="down" stroke="#3b82f6" strokeWidth={2} fill="url(#gDown)" />
                <Area type="monotone" dataKey="total" stroke="#22c55e" strokeWidth={2} fill="url(#gTotal)" name="total" />
              </AreaChart>
            </ResponsiveContainer>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-sm">年度热力图</CardTitle></CardHeader>
        <CardContent><Heatmap days={d.heatmap} /></CardContent>
      </Card>

      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-sm">分组统计（本期）</CardTitle>
          <Tabs value={groupMode} onValueChange={(v) => setGroupMode(v as any)}>
            <TabsList>
              <TabsTrigger value="rule">按转发</TabsTrigger>
              <TabsTrigger value="node">按节点</TabsTrigger>
            </TabsList>
          </Tabs>
        </CardHeader>
        <CardContent className="px-0 pb-0">
          <Table>
            <TableHeader><TableRow>
              <TableHead>名称</TableHead><TableHead>节点</TableHead><TableHead>下载</TableHead>
              <TableHead>上传</TableHead><TableHead>合计</TableHead><TableHead className="w-56">占比</TableHead><TableHead>连接</TableHead>
            </TableRow></TableHeader>
            <TableBody>
              {groups.length === 0 && (
                <TableRow><TableCell colSpan={7}><div className="py-10 text-center text-sm text-muted-foreground">本期没有流量数据</div></TableCell></TableRow>
              )}
              {groups.map((g) => {
                const v = g.up + g.down;
                const pct = (v / grand) * 100;
                return (
                  <TableRow key={g.tag}>
                    <TableCell className="font-medium">{g.tag}</TableCell>
                    <TableCell>{groupMode === "rule" ? (g.node_id === 0 ? "本机" : `#${g.node_id}`) : "-"}</TableCell>
                    <TableCell className="font-mono text-xs">{fmtBytes(g.down)}</TableCell>
                    <TableCell className="font-mono text-xs">{fmtBytes(g.up)}</TableCell>
                    <TableCell className="font-mono text-xs font-semibold">{fmtBytes(v)}</TableCell>
                    <TableCell>
                      <TrafficBar pct={pct} />
                      <div className="mt-1 text-[11px] text-muted-foreground">{pct.toFixed(1)}%</div>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{g.conns}</TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}
