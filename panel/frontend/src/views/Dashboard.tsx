import { useEffect, useState } from "react";
import { api, type Overview } from "@/api";
import { fmtBytes } from "@/lib/utils";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { AlertTriangle } from "lucide-react";

function Stat({ k, v, sub }: { k: string; v: any; sub?: string }) {
  return (
    <Card>
      <CardContent className="p-4">
        <div className="mb-1.5 text-xs text-muted-foreground">{k}</div>
        <div className="text-xl font-semibold tracking-tight">
          {v} {sub && <span className="text-xs font-normal text-muted-foreground">{sub}</span>}
        </div>
      </CardContent>
    </Card>
  );
}

export function Dashboard({ isAdmin }: { isAdmin: boolean }) {
  const [d, setD] = useState<Overview | null>(null);
  useEffect(() => {
    api<Overview>("/api/overview").then(setD).catch(() => {});
  }, []);

  if (!d) return <div className="py-16 text-center text-sm text-muted-foreground">加载中…</div>;
  const t = d.tuning;

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-xl font-semibold tracking-tight">仪表盘</h2>
        <p className="mt-1 text-xs text-muted-foreground">{d.version}</p>
      </div>
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
        <Stat k="转发规则" v={d.rules_running} sub={`运行 / ${d.rules_total} 总计`} />
        <Stat k="节点" v={d.nodes_online} sub={`在线 / ${d.nodes_total} 总计`} />
        <Stat k="累计流量" v={fmtBytes(d.traffic_total)} sub="自实例启动起" />
        <Stat k="用户" v={d.users_total} sub="个账号" />
        <Stat k="本机负载" v={`${d.machine.cpu_cores ?? 0} 核`} sub={d.machine.load_avg || "-"} />
        <Stat
          k="拥塞算法"
          v={t?.congestion_control || "-"}
          sub={t?.bbr_active ? "BBR" : t?.bbr_supported ? "支持未启用" : "内核不支持"}
        />
      </div>
      {d.over_quota?.length > 0 && (
        <Card className="border-destructive/50">
          <CardHeader className="pb-2">
            <CardTitle className="flex items-center gap-2 text-sm text-destructive">
              <AlertTriangle className="h-4 w-4" />超配额转发
            </CardTitle>
          </CardHeader>
          <CardContent className="text-xs text-muted-foreground">
            以下转发已达流量配额被自动停机：{d.over_quota.join("、")}。可在「转发管理」编辑配额后重新启动。
          </CardContent>
        </Card>
      )}
      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-sm">本机机器信息</CardTitle></CardHeader>
        <CardContent className="text-xs text-muted-foreground">
          系统 {d.machine.os || "-"} · 内核 {d.machine.kernel || "-"} · 内存 {fmtBytes(d.machine.mem_avail)} 可用 / {fmtBytes(d.machine.mem_total)}
        </CardContent>
      </Card>
    </div>
  );
}
