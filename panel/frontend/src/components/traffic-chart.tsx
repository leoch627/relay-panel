import { ResponsiveContainer, AreaChart, Area, XAxis, YAxis, CartesianGrid, Tooltip, type TooltipProps } from "recharts";
import type { TrafficPoint } from "@/api";
import { fmtBytes } from "@/lib/utils";

const pad = (n: number) => String(n).padStart(2, "0");

export function fmtTick(range: string, v: number) {
  const t = new Date(v);
  if (range === "24h") return `${pad(t.getHours())}:00`;
  return `${t.getMonth() + 1}/${t.getDate()}`;
}

function fmtLabel(range: string, v: number) {
  const t = new Date(v);
  if (range === "24h") return `${t.getMonth() + 1}月${t.getDate()}日 ${pad(t.getHours())}:00`;
  if (range === "365d") return `${t.getMonth() + 1}月${t.getDate()}日 起一周`;
  return `${t.getFullYear()}年${t.getMonth() + 1}月${t.getDate()}日`;
}

function ChartTooltip({ active, payload, label, range }: TooltipProps<number, string> & { range: string }) {
  if (!active || !payload?.length) return null;
  const p = payload[0].payload as { up: number; down: number };
  return (
    <div className="min-w-44 rounded-lg border bg-popover px-3 py-2.5 text-xs shadow-lg">
      <div className="mb-2 font-medium">{fmtLabel(range, Number(label))}</div>
      <div className="space-y-1.5">
        <div className="flex items-center justify-between gap-4"><span className="flex items-center gap-2 text-muted-foreground"><i className="h-2 w-2 rounded-full bg-chart-1" />下载</span><span className="tabular font-medium">{fmtBytes(p.down)}</span></div>
        <div className="flex items-center justify-between gap-4"><span className="flex items-center gap-2 text-muted-foreground"><i className="h-2 w-2 rounded-full bg-chart-2" />上传</span><span className="tabular font-medium">{fmtBytes(p.up)}</span></div>
        <div className="flex items-center justify-between gap-4 border-t pt-1.5"><span className="text-muted-foreground">合计</span><span className="tabular font-semibold">{fmtBytes(p.up + p.down)}</span></div>
      </div>
    </div>
  );
}

// 下载 + 上传堆叠面积图（下载在下、上传叠加在上，顶线即总量）
export function TrafficChart({ points, range, height = 280 }: { points: TrafficPoint[]; range: string; height?: number }) {
  const data = points.map((p) => ({ t: p.t * 1000, up: p.up, down: p.down }));
  return (
    <div style={{ height }}>
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 8, right: 4, left: 0, bottom: 0 }}>
          <defs>
            <linearGradient id="fillDown" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="hsl(var(--chart-1))" stopOpacity={0.32} />
              <stop offset="100%" stopColor="hsl(var(--chart-1))" stopOpacity={0.02} />
            </linearGradient>
            <linearGradient id="fillUp" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="hsl(var(--chart-2))" stopOpacity={0.35} />
              <stop offset="100%" stopColor="hsl(var(--chart-2))" stopOpacity={0.04} />
            </linearGradient>
          </defs>
          <CartesianGrid vertical={false} stroke="hsl(var(--border))" />
          <XAxis dataKey="t" tickFormatter={(v) => fmtTick(range, v)} tick={{ fill: "hsl(var(--muted-foreground))", fontSize: 11 }} axisLine={false} tickLine={false} minTickGap={36} tickMargin={8} />
          <YAxis tickFormatter={(v) => (v ? fmtBytes(v).replace(" ", "") : "0")} tick={{ fill: "hsl(var(--muted-foreground))", fontSize: 11 }} axisLine={false} tickLine={false} width={56} />
          <Tooltip content={<ChartTooltip range={range} />} cursor={{ stroke: "hsl(var(--muted-foreground))", strokeOpacity: 0.3, strokeDasharray: "4 4" }} />
          <Area type="monotone" dataKey="down" stackId="1" stroke="hsl(var(--chart-1))" strokeWidth={2} fill="url(#fillDown)" activeDot={{ r: 4, strokeWidth: 0 }} />
          <Area type="monotone" dataKey="up" stackId="1" stroke="hsl(var(--chart-2))" strokeWidth={2} fill="url(#fillUp)" activeDot={{ r: 4, strokeWidth: 0 }} />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

export function ChartLegend() {
  return (
    <div className="flex items-center gap-4 text-xs text-muted-foreground">
      <span className="flex items-center gap-1.5"><i className="h-2.5 w-2.5 rounded-sm bg-chart-1" />下载</span>
      <span className="flex items-center gap-1.5"><i className="h-2.5 w-2.5 rounded-sm bg-chart-2" />上传</span>
    </div>
  );
}
