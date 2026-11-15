import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { ArrowUpCircle, ExternalLink, Loader2, RefreshCw, Server, X } from "lucide-react";
import { api, errMsg, type UpdateInfo, type UpdateNode } from "@/api";
import { cn, fmtRelative } from "@/lib/utils";
import { usePolling } from "@/lib/usePolling";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { StatusDot } from "@/components/common";
import { confirm } from "@/components/confirm";

function nodeState(n: UpdateNode): { text: string; tone: "muted" | "warning" | "success" | "destructive" } {
  if (n.upgrade_to) return n.error ? { text: `升级到 ${n.upgrade_to} 失败，稍后重试：${n.error}`, tone: "destructive" } : { text: `等待节点升级到 ${n.upgrade_to}`, tone: "warning" };
  if (!n.version) return { text: "尚未上报版本", tone: "muted" };
  if (!n.outdated) return { text: "已是最新", tone: "success" };
  if (!n.supported) return { text: "该版本不支持远程升级，请在节点上重新执行安装命令", tone: "warning" };
  return { text: "可升级", tone: "warning" };
}

const stateClass = { muted: "text-muted-foreground", warning: "text-warning", success: "text-success", destructive: "text-destructive" };

export function UpdateCard() {
  const request = useCallback((signal: AbortSignal) => api<UpdateInfo>("/api/system/update", { signal }), []);
  const { data, error, reload } = usePolling(request, 5000);
  const [busy, setBusy] = useState<string | null>(null);
  // 发起主控升级时记下旧版本：轮询到版本变化即视为升级完成
  const [upgradingFrom, setUpgradingFrom] = useState<string | null>(null);

  useEffect(() => {
    if (!upgradingFrom || !data) return;
    if (data.current !== upgradingFrom) {
      toast.success(`主控已升级到 ${data.current}`);
      setUpgradingFrom(null);
    } else if (data.status.phase === "failed") {
      toast.error(`升级失败：${data.status.message}`);
      setUpgradingFrom(null);
    }
  }, [data, upgradingFrom]);

  if (!data) {
    return <Card className="mb-4 p-5 text-[13px] text-muted-foreground">{error ? `读取版本信息失败：${error}` : "正在读取版本信息…"}</Card>;
  }

  const latest = data.latest;
  const working = upgradingFrom !== null || data.status.phase === "downloading" || data.status.phase === "restarting";
  const upgradable = data.nodes.filter((n) => n.outdated && n.supported && !n.upgrade_to);

  const check = async () => {
    setBusy("check");
    try {
      const next = await api<UpdateInfo>("/api/system/update?refresh=1");
      if (next.check_error) toast.error(`检查更新失败：${next.check_error}`);
      else toast.success(next.has_update ? `发现新版本 ${next.latest?.tag}` : "当前已是最新版本");
      reload();
    } catch (e) { toast.error(errMsg(e)); } finally { setBusy(null); }
  };

  const upgradeMaster = async () => {
    if (!latest) return;
    if (!(await confirm({
      title: `升级主控到 ${latest.tag}？`,
      description: "将从 GitHub 发布页下载并校验新版本，替换面板程序后自动重启，页面会短暂不可用。\n本机转发实例不受影响；被控节点需要在下方单独升级。",
      confirmText: "升级并重启",
    }))) return;
    setBusy("master");
    try {
      await api("/api/system/update", { body: { version: latest.tag } });
      setUpgradingFrom(data.current);
      reload();
    } catch (e) { toast.error(errMsg(e)); } finally { setBusy(null); }
  };

  const upgradeNodes = async (body: { ids?: number[]; all?: boolean; cancel?: boolean }, key: string) => {
    if (!body.cancel && body.all && !(await confirm({
      title: `升级 ${upgradable.length} 个节点到 ${latest?.tag}？`,
      description: "节点会在下次轮询时自行下载并校验新版本，然后重启代理。systemd 节点的转发不中断；无 systemd 的直跑节点会短暂重启转发实例。",
      confirmText: "升级节点",
    }))) return;
    setBusy(key);
    try {
      const r = await api<{ queued: number; skipped: number }>("/api/nodes/upgrade", { body });
      if (body.cancel) toast.success("已取消");
      else if (r.queued) toast.success(`已通知 ${r.queued} 个节点升级${r.skipped ? `，跳过 ${r.skipped} 个` : ""}`);
      else toast.message("没有需要升级的节点");
      reload();
    } catch (e) { toast.error(errMsg(e)); } finally { setBusy(null); }
  };

  const tone = data.has_update ? "bg-primary" : latest ? "bg-success" : "bg-muted-foreground/40";

  return (
    <Card className={cn("relative mb-4 overflow-hidden", data.has_update && "border-primary/30")}>
      <div className={cn("absolute inset-y-0 left-0 w-1", tone)} />
      <div className="flex flex-wrap items-center gap-5 p-5 pl-6">
        <div className={cn("grid h-12 w-12 shrink-0 place-items-center rounded-xl", data.has_update ? "bg-primary/10 text-primary" : latest ? "bg-success/10 text-success" : "bg-muted text-muted-foreground")}>
          <ArrowUpCircle className="h-6 w-6" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-base font-semibold">版本与升级</span>
            <Badge variant="secondary" className="font-mono">{data.current}</Badge>
            {data.has_update && latest ? <Badge>新版本 {latest.tag}</Badge>
              : latest ? <Badge variant="success">已是最新</Badge>
              : <Badge variant="warning">未获取到发布信息</Badge>}
          </div>
          <p className="mt-1 text-[13px] text-muted-foreground">
            {working ? (error ? "主控正在重启，稍候自动恢复…" : data.status.message || "正在升级…")
              : data.check_error ? `检查更新失败：${data.check_error}`
              : data.has_update ? "从 GitHub 发布页下载，按发布内的 SHA256 清单校验后替换并重启。"
              : `发布来源 ${data.repo}${data.checked_at ? ` · ${fmtRelative(data.checked_at)}检查` : ""}`}
            {latest && !working && (
              <a href={latest.url} target="_blank" rel="noreferrer" className="ml-2 inline-flex items-center gap-1 text-primary hover:underline">
                发布说明<ExternalLink className="h-3 w-3" />
              </a>
            )}
          </p>
          {data.has_update && !data.self_update && <p className="mt-1 text-[13px] text-warning">主控无法在线升级：{data.self_update_reason}</p>}
          {data.status.phase === "failed" && !working && <p className="mt-1 text-[13px] text-destructive">上次升级失败：{data.status.message}</p>}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" disabled={busy !== null || working} onClick={check}>
            {busy === "check" ? <Loader2 className="animate-spin" /> : <RefreshCw />}检查更新
          </Button>
          {data.has_update && (
            <Button disabled={!data.self_update || busy !== null || working} onClick={upgradeMaster}>
              {working || busy === "master" ? <Loader2 className="animate-spin" /> : <ArrowUpCircle />}升级主控
            </Button>
          )}
        </div>
      </div>

      {data.has_update && latest?.notes && (
        <details className="border-t px-6 py-3 text-[13px]">
          <summary className="cursor-pointer select-none text-muted-foreground">{latest.tag} 更新内容</summary>
          <pre className="mt-2 max-h-56 overflow-auto whitespace-pre-wrap break-words font-sans leading-relaxed">{latest.notes}</pre>
        </details>
      )}

      {data.nodes.length > 0 && (
        <div className="border-t px-6 py-4">
          <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
            <span className="flex items-center gap-2 text-[13px] font-medium"><Server className="h-4 w-4 text-muted-foreground" />被控节点</span>
            {upgradable.length > 0 && (
              <Button size="sm" variant="outline" disabled={busy !== null} onClick={() => upgradeNodes({ all: true }, "all")}>
                {busy === "all" ? <Loader2 className="animate-spin" /> : <ArrowUpCircle />}升级全部（{upgradable.length}）
              </Button>
            )}
          </div>
          <ul className="divide-y">
            {data.nodes.map((n) => {
              const st = nodeState(n);
              return (
                <li key={n.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2 text-[13px]">
                  <div className="flex min-w-0 flex-1 items-center gap-3 sm:max-w-[45%] sm:flex-none">
                    <StatusDot tone={n.online ? "success" : "secondary"} />
                    <span className="min-w-0 truncate font-medium">{n.name}</span>
                    <span className="font-mono text-xs text-muted-foreground">{n.version || "-"}</span>
                  </div>
                  {/* 窄屏下状态文字单独占一行，避免被名称和按钮挤成竖排 */}
                  <span className={cn("order-last min-w-0 basis-full break-words pl-5 sm:order-none sm:basis-0 sm:flex-1 sm:pl-0", stateClass[st.tone])}>{st.text}</span>
                  {n.upgrade_to ? (
                    <Button size="xs" variant="ghost" disabled={busy !== null} onClick={() => upgradeNodes({ ids: [n.id], cancel: true }, `n${n.id}`)}>
                      {busy === `n${n.id}` ? <Loader2 className="animate-spin" /> : <X />}取消
                    </Button>
                  ) : n.outdated && n.supported ? (
                    <Button size="xs" variant="outline" disabled={busy !== null} onClick={() => upgradeNodes({ ids: [n.id] }, `n${n.id}`)}>
                      {busy === `n${n.id}` ? <Loader2 className="animate-spin" /> : <ArrowUpCircle />}升级
                    </Button>
                  ) : null}
                </li>
              );
            })}
          </ul>
        </div>
      )}
    </Card>
  );
}
