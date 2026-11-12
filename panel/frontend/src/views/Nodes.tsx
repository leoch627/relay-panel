import { useCallback, useState } from "react";
import { toast } from "sonner";
import { Cpu, HardDrive, KeyRound, Loader2, MoreHorizontal, Plus, RotateCcw, Server, Terminal, Trash2, Clock, Box, ShieldAlert } from "lucide-react";
import { api, errMsg, type NodeInfo } from "@/api";
import { escTime, fmtBytes, fmtRelative } from "@/lib/utils";
import { usePolling } from "@/lib/usePolling";
import { nodeInstallCommand, type TLSInfo, type TLSMode } from "@/lib/nodeInstall";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Meter, Skeleton } from "@/components/ui/misc";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tooltip } from "@/components/ui/tooltip";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { CodeBlock, CopyButton, EmptyState, ErrorBanner, PageHeader, StatusDot, nodeTone } from "@/components/common";
import { confirm } from "@/components/confirm";

type Panel = { kind: "install"; node: { name: string; key: string }; fresh?: boolean } | { kind: "key"; name: string; key: string; regenerated?: boolean };

function NodeCard({ n, tlsReady, onInstall, onShowKey, onRegen, onDelete }: {
  n: NodeInfo; tlsReady: boolean; onInstall: () => void; onShowKey: () => void; onRegen: () => void; onDelete: () => void;
}) {
  const m = n.report?.machine || {};
  const memUsed = m.mem_total ? m.mem_total - (m.mem_avail || 0) : 0;
  const tone = nodeTone(n);
  const reported = !!n.report?.at;
  return (
    <Card className="flex flex-col">
      <div className="flex items-start gap-3 p-5 pb-4">
        <div className="relative grid h-10 w-10 shrink-0 place-items-center rounded-lg border bg-muted/50">
          <Server className="h-[18px] w-[18px] text-muted-foreground" />
          <StatusDot tone={tone} live={n.online && !n.deleting} className="absolute -right-0.5 -top-0.5 ring-2 ring-card" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate font-semibold">{n.name}</span>
            <span className="font-mono text-[11px] text-muted-foreground">#{n.id}</span>
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-1.5">
            {n.deleting ? <Badge variant="warning">清理中{n.online ? "" : " · 等待上线"}</Badge>
              : n.online ? <Badge variant="success">在线</Badge> : <Badge variant="destructive">离线</Badge>}
            <Badge variant="secondary">{n.rule_count} 条转发</Badge>
          </div>
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button size="icon-sm" variant="ghost" className="-mr-2 -mt-1 text-muted-foreground"><MoreHorizontal /><span className="sr-only">更多</span></Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-40">
            <DropdownMenuItem disabled={!n.key || !tlsReady} onSelect={onInstall}><Terminal />安装命令</DropdownMenuItem>
            <DropdownMenuItem disabled={!n.key} onSelect={onShowKey}><KeyRound />查看密钥</DropdownMenuItem>
            <DropdownMenuItem disabled={n.deleting} onSelect={onRegen}><RotateCcw />重置密钥</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem destructive disabled={n.deleting} onSelect={onDelete}><Trash2 />删除节点</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <div className="flex-1 space-y-3 px-5 pb-4 text-[13px]">
        {reported ? <>
          <div>
            <div className="mb-1.5 flex items-center justify-between"><span className="flex items-center gap-2 text-muted-foreground"><HardDrive className="h-3.5 w-3.5" />内存</span><span className="tabular text-xs">{fmtBytes(memUsed)} / {fmtBytes(m.mem_total)}</span></div>
            <Meter value={m.mem_total ? (memUsed / m.mem_total) * 100 : 0} />
          </div>
          <div className="flex items-center justify-between"><span className="flex items-center gap-2 text-muted-foreground"><Cpu className="h-3.5 w-3.5" />CPU · 负载</span><span className="tabular text-xs">{m.cpu_cores ?? "?"} 核 · {m.load_avg?.split(" ")[0] || "-"}</span></div>
          <div className="flex items-center justify-between gap-3"><span className="flex shrink-0 items-center gap-2 text-muted-foreground"><Box className="h-3.5 w-3.5" />系统</span><span className="truncate text-xs">{m.os || "-"}</span></div>
          <div className="flex items-center justify-between gap-3"><span className="flex shrink-0 items-center gap-2 text-muted-foreground"><Terminal className="h-3.5 w-3.5" />tcp_pool</span><span className="truncate font-mono text-[11px]">{(n.report.tcp_pool || "未上报").split("(")[0]}</span></div>
        </> : (
          <div className="flex h-full min-h-24 flex-col items-center justify-center gap-1 rounded-lg border border-dashed text-center">
            <span className="text-xs font-medium">等待节点首次上报</span>
            <span className="text-[11px] text-muted-foreground">在被控机执行安装命令后自动上线</span>
          </div>
        )}
      </div>

      <div className="flex items-center justify-between gap-2 border-t px-5 py-3">
        <Tooltip content={n.last_seen ? escTime(n.last_seen) : undefined}>
          <span className="flex items-center gap-1.5 text-xs text-muted-foreground"><Clock className="h-3.5 w-3.5" />{n.last_seen ? `心跳 ${fmtRelative(n.last_seen)}` : "从未连接"}</span>
        </Tooltip>
        <Button size="xs" variant={reported && !n.deleting ? "ghost" : "outline"} disabled={!n.key || !tlsReady} onClick={onInstall}>
          <Terminal />{n.deleting ? "升级以完成清理" : "安装命令"}
        </Button>
      </div>
    </Card>
  );
}

export function Nodes() {
  const [addOpen, setAddOpen] = useState(false);
  const [name, setName] = useState("");
  const [adding, setAdding] = useState(false);
  const [panel, setPanel] = useState<Panel | null>(null);
  const [tlsMode, setTLSMode] = useState<TLSMode>("auto");
  const request = useCallback(async (signal: AbortSignal) => {
    const [nodeData, tls] = await Promise.all([
      api<{ nodes: NodeInfo[] }>("/api/nodes", { signal }),
      api<TLSInfo>("/api/tls", { signal }),
    ]);
    return { nodes: nodeData.nodes || [], tls };
  }, []);
  const { data, error, reload } = usePolling(request);
  const nodes = data?.nodes ?? null;
  const tls = data?.tls ?? null;
  const online = nodes?.filter((n) => n.online).length ?? 0;

  const showInstall = (node: { name: string; key: string }, fresh = false) => { setTLSMode("auto"); setPanel({ kind: "install", node, fresh }); };

  let command = "";
  let commandError = "";
  if (panel?.kind === "install") {
    try { command = nodeInstallCommand(location.origin, panel.node.key, tls, tlsMode); }
    catch (e) { commandError = errMsg(e); }
  }

  const addNode = async (e: React.FormEvent) => {
    e.preventDefault();
    setAdding(true);
    try {
      const d = await api<{ node: { key: string; name: string } }>("/api/nodes", { body: { name: name.trim() } });
      setAddOpen(false); setName(""); reload();
      showInstall(d.node, true);
    } catch (err) { toast.error(errMsg(err)); } finally { setAdding(false); }
  };

  const regen = async (n: NodeInfo) => {
    if (!(await confirm({ title: `重置 ${n.name} 的密钥？`, description: "旧密钥立即失效。节点上已有的转发保持不变，但需要用新密钥更新节点配置并重启服务后才能继续同步。", confirmText: "重置密钥", destructive: true }))) return;
    try {
      const d = await api<{ key: string }>(`/api/nodes/${n.id}/key`, { method: "POST" });
      setPanel({ kind: "key", name: n.name, key: d.key, regenerated: true });
      reload();
    } catch (e) { toast.error(errMsg(e)); }
  };

  const del = async (n: NodeInfo) => {
    if (!(await confirm({
      title: `删除节点 ${n.name}？`,
      description: `其名下 ${n.rule_count} 条转发规则会一并删除。\n节点确认清理完成后才会真正移除；离线节点需等待重新上线，旧版节点需先升级到 v1.1.0。`,
      confirmText: "删除节点", destructive: true,
    }))) return;
    try {
      const result = await api<{ pending?: boolean }>(`/api/nodes/${n.id}`, { method: "DELETE" });
      toast.success(result.pending ? `正在清理节点 ${n.name}，等待节点确认` : `已删除节点 ${n.name}`);
      reload();
    } catch (e) { toast.error(errMsg(e)); }
  };

  return (
    <div>
      <PageHeader
        title="节点"
        description="主控统一下发转发配置；被控节点出站轮询（约 5 秒），无需开放入站端口，可以部署在 NAT 之后。"
        actions={<Button size="sm" disabled={!tls} onClick={() => setAddOpen(true)}><Plus />添加节点</Button>}
      >
        {nodes && nodes.length > 0 && (
          <div className="mt-3 flex items-center gap-4 text-xs text-muted-foreground">
            <span className="flex items-center gap-1.5"><StatusDot tone="success" />{online} 在线</span>
            <span className="flex items-center gap-1.5"><StatusDot tone="destructive" />{nodes.length - online} 离线</span>
          </div>
        )}
      </PageHeader>
      <ErrorBanner message={error} />

      {nodes === null ? (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-64 rounded-xl" />)}</div>
      ) : nodes.length === 0 ? (
        <Card>
          <EmptyState
            icon={Server}
            title="还没有被控节点"
            description="添加节点会生成一个密钥和一键安装命令，在被控机上以 root 执行即可接管该机器的转发。"
            action={<Button size="sm" disabled={!tls} onClick={() => setAddOpen(true)}><Plus />添加第一个节点</Button>}
          />
        </Card>
      ) : (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {nodes.map((n) => (
            <NodeCard
              key={n.id} n={n} tlsReady={!!tls}
              onInstall={() => showInstall({ name: n.name, key: n.key! })}
              onShowKey={() => setPanel({ kind: "key", name: n.name, key: n.key || "" })}
              onRegen={() => regen(n)}
              onDelete={() => del(n)}
            />
          ))}
        </div>
      )}

      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader>
            <DialogTitle>添加节点</DialogTitle>
            <DialogDescription>创建后会生成节点密钥与安装命令。</DialogDescription>
          </DialogHeader>
          <form id="node-form" onSubmit={addNode} className="space-y-2">
            <Label htmlFor="node-name">节点名称</Label>
            <Input id="node-name" autoFocus value={name} placeholder="如 HK-line1" maxLength={64} onChange={(e) => setName(e.target.value)} required />
            <p className="text-xs text-muted-foreground">1-64 个字符，便于识别线路即可</p>
          </form>
          <DialogFooter>
            <Button variant="outline" onClick={() => setAddOpen(false)}>取消</Button>
            <Button type="submit" form="node-form" disabled={adding || !name.trim()}>{adding && <Loader2 className="animate-spin" />}创建并生成密钥</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={panel?.kind === "install"} onOpenChange={(v) => !v && setPanel(null)}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>{panel?.kind === "install" && (panel.fresh ? `节点 ${panel.node.name} 已创建` : `安装命令 · ${panel.node.name}`)}</DialogTitle>
            <DialogDescription>在被控机上以 root 身份执行以下命令，完成后节点会自动上线。</DialogDescription>
          </DialogHeader>
          {location.protocol === "https:" && (
            <div className="space-y-2">
              <Label>节点连接主控时的证书校验</Label>
              <Select value={tlsMode} onValueChange={(v) => setTLSMode(v as TLSMode)}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="auto">自动（{tls?.pin_recommended ? "固定自签证书指纹" : "公共 CA 校验"}）</SelectItem>
                  <SelectItem value="public">公共证书 / Cloudflare / HTTPS 反代</SelectItem>
                  <SelectItem value="pin" disabled={!tls?.fingerprint}>直连主控自签证书（固定指纹）</SelectItem>
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">域名前有 HTTPS 反向代理时请选公共证书校验；仅在直连主控时固定源站指纹。</p>
            </div>
          )}
          {commandError ? <p className="rounded-lg border border-destructive/25 bg-destructive/5 p-3 text-[13px] text-destructive">{commandError}</p> : <CodeBlock code={command} />}
          <DialogFooter>
            <Button variant="outline" onClick={() => setPanel(null)}>完成</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={panel?.kind === "key"} onOpenChange={(v) => !v && setPanel(null)}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>{panel?.kind === "key" && (panel.regenerated ? `${panel.name} 的新密钥` : `节点密钥 · ${panel.name}`)}</DialogTitle>
            <DialogDescription>密钥等同于节点的登录凭据，请勿泄露。</DialogDescription>
          </DialogHeader>
          {panel?.kind === "key" && (
            <>
              <div className="flex items-center gap-2 rounded-lg border bg-muted/40 p-1 pl-3">
                <code className="min-w-0 flex-1 break-all font-mono text-[13px]">{panel.key || "（仅管理员可见）"}</code>
                {panel.key && <CopyButton text={panel.key} />}
              </div>
              {panel.regenerated && (
                <div className="flex gap-2.5 rounded-lg border border-warning/30 bg-warning/5 p-3 text-[13px]">
                  <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0 text-warning" />
                  <span>旧密钥已失效，节点现有转发保持不变。请用新密钥更新该节点的配置后重启服务，节点才能继续同步。</span>
                </div>
              )}
            </>
          )}
          <DialogFooter><Button variant="outline" onClick={() => setPanel(null)}>关闭</Button></DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
