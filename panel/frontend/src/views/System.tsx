import { useCallback, useState } from "react";
import { toast } from "sonner";
import { Check, Cpu, Feather, Gauge, HardDrive, Layers, Loader2, Rocket, Scale, Zap } from "lucide-react";
import { api, errMsg, type MachineInfo, type TuningStatus } from "@/api";
import { cn, fmtBytes } from "@/lib/utils";
import { usePolling } from "@/lib/usePolling";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Meter } from "@/components/ui/misc";
import { ErrorBanner, LoadingState, PageHeader } from "@/components/common";
import { confirm } from "@/components/confirm";

const PROFILE_ICON: Record<string, typeof Feather> = { conservative: Feather, standard: Scale, aggressive: Rocket };

export function System() {
  const request = useCallback((signal: AbortSignal) => api<{ tuning: TuningStatus; machine: MachineInfo }>("/api/system", { signal }), []);
  const { data, error, reload } = usePolling(request, 15000);
  const [busy, setBusy] = useState<string | null>(null);
  const t = data?.tuning;
  const m = data?.machine;
  if (!t || !m) return <LoadingState error={error} />;

  const applyBBR = async () => {
    setBusy("bbr");
    try { await api("/api/system/bbr", { method: "POST" }); toast.success("BBR + fq 已应用"); reload(); }
    catch (e) { toast.error(errMsg(e)); } finally { setBusy(null); }
  };
  const applyProfile = async (name: string, label: string) => {
    if (!(await confirm({ title: `应用「${label}」调优档位？`, description: `将写入 ${t.config_file} 并立即通过 sysctl 生效，同时启用 BBR + fq。${t.file_exists && !t.file_managed ? "\n当前文件不是面板创建的，应用后会被接管覆盖。" : ""}`, confirmText: "应用" }))) return;
    setBusy(name);
    try {
      await api("/api/system/tuning", { body: { profile: name, bbr: true } });
      toast.success(`已应用「${label}」调优`); reload();
    } catch (e) { toast.error(errMsg(e)); } finally { setBusy(null); }
  };

  const memUsed = m.mem_total ? m.mem_total - (m.mem_avail || 0) : 0;

  return (
    <div>
      <PageHeader title="系统调优" description="主控服务器的 BBR 拥塞控制与 TCP 内核参数。仅管理员可操作，被控节点不受影响。" />
      <ErrorBanner message={error} />

      <Card className={cn("relative overflow-hidden", t.bbr_active ? "" : "border-primary/30")}>
        <div className={cn("absolute inset-y-0 left-0 w-1", t.bbr_active ? "bg-success" : t.bbr_supported ? "bg-primary" : "bg-muted-foreground/40")} />
        <div className="flex flex-wrap items-center gap-5 p-5 pl-6">
          <div className={cn("grid h-12 w-12 shrink-0 place-items-center rounded-xl", t.bbr_active ? "bg-success/10 text-success" : "bg-primary/10 text-primary")}>
            <Zap className="h-6 w-6" />
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-base font-semibold">BBR 拥塞控制</span>
              {t.bbr_active ? <Badge variant="success">已生效</Badge> : t.bbr_supported ? <Badge variant="warning">支持但未启用</Badge> : <Badge variant="destructive">内核不支持</Badge>}
            </div>
            <p className="mt-1 text-[13px] text-muted-foreground">
              {t.bbr_active ? "当前使用 BBR + " + (t.qdisc || "fq") + "，高延迟、有丢包的跨境链路吞吐更稳定。"
                : t.bbr_supported ? "建议开启：在高延迟或有丢包的线路上通常能显著提升吞吐。"
                : "当前内核未提供 bbr 模块，需升级到 4.9 以上内核。"}
            </p>
          </div>
          <Button disabled={!t.bbr_supported || busy !== null} variant={t.bbr_active ? "outline" : "default"} onClick={applyBBR}>
            {busy === "bbr" ? <Loader2 className="animate-spin" /> : <Zap />}{t.bbr_active ? "重新应用" : "开启 BBR + fq"}
          </Button>
        </div>
      </Card>

      <div className="mt-4 grid gap-4 lg:grid-cols-3">
        <Card>
          <CardHeader><CardTitle>网络栈</CardTitle><CardDescription>当前内核生效的参数</CardDescription></CardHeader>
          <CardContent className="space-y-4 text-[13px]">
            <div className="flex items-center justify-between"><span className="flex items-center gap-2 text-muted-foreground"><Gauge className="h-4 w-4" />拥塞算法</span><span className="font-mono text-xs font-medium">{t.congestion_control || "-"}</span></div>
            <div className="flex items-center justify-between"><span className="flex items-center gap-2 text-muted-foreground"><Layers className="h-4 w-4" />队列 qdisc</span><span className="font-mono text-xs font-medium">{t.qdisc || "-"}</span></div>
            <div>
              <div className="mb-2 text-muted-foreground">可用算法</div>
              <div className="flex flex-wrap gap-1.5">
                {t.available_cc?.length ? t.available_cc.map((c) => <Badge key={c} variant={c === t.congestion_control ? "default" : "secondary"} className="font-mono">{c}</Badge>) : <span className="text-xs text-muted-foreground">-</span>}
              </div>
            </div>
          </CardContent>
        </Card>
        <Card className="lg:col-span-2">
          <CardHeader><CardTitle>主机</CardTitle><CardDescription className="truncate">{m.hostname || "主控服务器"}</CardDescription></CardHeader>
          <CardContent className="grid gap-5 sm:grid-cols-2">
            <div className="space-y-4 text-[13px]">
              <div>
                <div className="mb-1.5 flex items-center justify-between"><span className="flex items-center gap-2 text-muted-foreground"><HardDrive className="h-4 w-4" />内存</span><span className="tabular text-xs">{fmtBytes(memUsed)} / {fmtBytes(m.mem_total)}</span></div>
                <Meter value={m.mem_total ? (memUsed / m.mem_total) * 100 : 0} />
              </div>
              <div className="flex items-center justify-between"><span className="flex items-center gap-2 text-muted-foreground"><Cpu className="h-4 w-4" />CPU</span><span className="tabular text-xs">{m.cpu_cores || 0} 核 · 负载 {m.load_avg || "-"}</span></div>
            </div>
            <div className="grid grid-cols-2 gap-3 rounded-lg bg-muted/50 p-3 text-xs">
              <div className="col-span-2"><div className="text-muted-foreground">系统</div><div className="mt-0.5 truncate font-medium">{m.os || "-"}</div></div>
              <div className="col-span-2"><div className="text-muted-foreground">内核</div><div className="mt-0.5 truncate font-mono">{t.kernel || m.kernel || "-"}</div></div>
            </div>
          </CardContent>
        </Card>
      </div>

      <div className="mb-3 mt-8 flex flex-wrap items-end justify-between gap-2">
        <div>
          <h2 className="text-base font-semibold">TCP 调优档位</h2>
          <p className="mt-1 text-[13px] text-muted-foreground">写入 <code className="font-mono text-xs">{t.config_file}</code> 并立即生效；所有档位默认搭配 BBR + fq。</p>
        </div>
        {t.file_exists && <Badge variant={t.file_managed ? "success" : "warning"}>{t.file_managed ? "调优文件由面板管理" : "存在非面板管理的调优文件"}</Badge>}
      </div>
      <div className="grid gap-4 md:grid-cols-3">
        {t.profiles?.map((p) => {
          const Icon = PROFILE_ICON[p.name] || Gauge;
          const recommended = p.name === "standard";
          return (
            <Card key={p.name} className={cn("flex flex-col p-5 transition-shadow hover:shadow-md", recommended && "ring-1 ring-primary/30")}>
              <div className="flex items-start justify-between">
                <div className={cn("grid h-10 w-10 place-items-center rounded-lg", recommended ? "bg-primary/10 text-primary" : "bg-muted text-muted-foreground")}><Icon className="h-5 w-5" /></div>
                {recommended && <Badge>推荐</Badge>}
              </div>
              <div className="mt-4 flex items-baseline gap-2"><span className="font-semibold">{p.label}</span><span className="font-mono text-xs text-muted-foreground">{p.name}</span></div>
              <p className="mt-1.5 flex-1 text-[13px] leading-relaxed text-muted-foreground">{p.desc}</p>
              <Button className="mt-5" size="sm" variant={recommended ? "default" : "outline"} disabled={busy !== null} onClick={() => applyProfile(p.name, p.label)}>
                {busy === p.name ? <Loader2 className="animate-spin" /> : <Check />}应用此档位
              </Button>
            </Card>
          );
        })}
      </div>
    </div>
  );
}
