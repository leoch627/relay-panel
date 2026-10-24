import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, type MachineInfo, type TuningStatus } from "@/api";
import { fmtBytes } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Zap } from "lucide-react";

export function System() {
  const [t, setT] = useState<TuningStatus | null>(null);
  const [m, setM] = useState<MachineInfo | null>(null);

  const load = useCallback(() => {
    api<{ tuning: TuningStatus; machine: MachineInfo }>("/api/system")
      .then((d) => { setT(d.tuning); setM(d.machine); })
      .catch(() => {});
  }, []);
  useEffect(load, [load]);

  if (!t || !m) return <div className="py-16 text-center text-sm text-muted-foreground">加载中…</div>;

  const applyBBR = async () => {
    try { await api("/api/system/bbr", { method: "POST" }); toast.success("BBR 已应用"); load(); }
    catch (e: any) { toast.error(e.message); }
  };
  const applyProfile = async (name: string) => {
    try {
      await api("/api/system/tuning", { body: { profile: name, bbr: true } });
      toast.success(`已应用「${name}」调优`); load();
    } catch (e: any) { toast.error(e.message); }
  };

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-xl font-semibold tracking-tight">系统调优</h2>
        <p className="mt-1 text-xs text-muted-foreground">BBR 拥塞控制与 TCP 内核参数（仅管理员可操作）</p>
      </div>

      <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
        <Card><CardContent className="p-4">
          <div className="mb-1.5 text-xs text-muted-foreground">拥塞算法</div>
          <div className="flex items-center gap-2 text-lg font-semibold">
            {t.congestion_control || "-"}
            {t.bbr_active ? <Badge variant="success">BBR 已生效</Badge>
              : t.bbr_supported ? <Badge variant="warning">支持未启用</Badge>
              : <Badge variant="destructive">内核不支持</Badge>}
          </div>
        </CardContent></Card>
        <Card><CardContent className="p-4">
          <div className="mb-1.5 text-xs text-muted-foreground">队列算法 qdisc</div>
          <div className="text-lg font-semibold">{t.qdisc || "-"}</div>
        </CardContent></Card>
        <Card><CardContent className="p-4">
          <div className="mb-1.5 text-xs text-muted-foreground">内核版本</div>
          <div className="text-sm font-semibold leading-8">{t.kernel || "-"}</div>
        </CardContent></Card>
        <Card><CardContent className="p-4">
          <div className="mb-1.5 text-xs text-muted-foreground">CPU / 负载</div>
          <div className="text-lg font-semibold">{m.cpu_cores || 0} 核 <span className="text-xs font-normal text-muted-foreground">{m.load_avg || "-"}</span></div>
        </CardContent></Card>
        <Card><CardContent className="p-4">
          <div className="mb-1.5 text-xs text-muted-foreground">内存</div>
          <div className="text-lg font-semibold">{fmtBytes(m.mem_avail)} <span className="text-xs font-normal text-muted-foreground">可用 / {fmtBytes(m.mem_total)}</span></div>
        </CardContent></Card>
        <Card><CardContent className="p-4">
          <div className="mb-1.5 text-xs text-muted-foreground">系统</div>
          <div className="text-sm font-semibold leading-8">{m.os || m.hostname || "-"}</div>
        </CardContent></Card>
      </div>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm">拥塞控制</CardTitle>
          <CardDescription>可用算法：{t.available_cc?.join(" · ") || "-"}</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap items-center gap-3">
          <Button size="sm" disabled={!t.bbr_supported} onClick={applyBBR}>
            <Zap className="h-4 w-4" />{t.bbr_active ? "重新应用 BBR" : "开启 BBR + fq"}
          </Button>
          {!t.bbr_supported && <span className="text-xs text-muted-foreground">当前内核未提供 bbr 模块</span>}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm">TCP 调优档位</CardTitle>
          <CardDescription>写入 {t.config_file} 并立即生效；所有档位默认搭配 BBR + fq。</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {t.profiles?.map((p) => (
            <div key={p.name} className="flex items-center justify-between gap-3 rounded-lg border p-3.5">
              <div>
                <b className="text-sm">{p.label}</b> <span className="font-mono text-xs text-muted-foreground">{p.name}</span>
                <div className="mt-0.5 text-xs text-muted-foreground">{p.desc}</div>
              </div>
              <Button size="sm" variant="secondary" onClick={() => applyProfile(p.name)}>应用</Button>
            </div>
          ))}
          {t.file_exists && (
            <p className="text-xs text-muted-foreground">
              {t.file_managed ? "当前调优文件由面板管理。" : "存在非面板管理的调优文件，应用档位后将接管该文件。"}
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
