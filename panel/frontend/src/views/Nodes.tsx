import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, type NodeInfo } from "@/api";
import { fmtBytes } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Copy, KeyRound, Plus, RotateCcw, Terminal } from "lucide-react";

export function Nodes() {
  const [nodes, setNodes] = useState<NodeInfo[] | null>(null);
  const [tls, setTls] = useState<{ enabled: boolean; fingerprint: string } | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  const [name, setName] = useState("");
  const [cmd, setCmd] = useState<{ title: string; text: string } | null>(null);

  const load = useCallback(() => {
    api<{ nodes: NodeInfo[] }>("/api/nodes").then((d) => setNodes(d.nodes)).catch(() => setNodes([]));
  }, []);
  useEffect(() => {
    load();
    api<{ enabled: boolean; fingerprint: string }>("/api/tls").then(setTls).catch(() => {});
  }, [load]);

  const master = location.origin;
  const tlsEnv = tls?.enabled && tls.fingerprint ? ` PANEL_TLS_FP=${tls.fingerprint}` : "";

  const slaveCmd = (n: NodeInfo) =>
    `# 在被控机（root）执行：\n` +
    `curl -fsSL https://raw.githubusercontent.com/leoch627/relay-panel/main/panel_install.sh -o panel_install.sh\n` +
    `PANEL_SLAVE=1 PANEL_MASTER_URL=${master} PANEL_NODE_KEY=${n.key}${tlsEnv} bash panel_install.sh` +
    (tlsEnv ? `\n\n# 主控证书 SHA256 指纹：\n# ${tls!.fingerprint}` : `\n\n# 注意：主控未启用 TLS，节点通信为明文 HTTP`);

  const addNode = async () => {
    try {
      const d = await api<{ node: { key: string; name: string } }>("/api/nodes", { body: { name: name.trim() } });
      setAddOpen(false); setName(""); load();
      setCmd({
        title: `节点已创建 · ${d.node.name}`,
        text:
          `# 1) 在被控机（root）执行：\n` +
          `curl -fsSL https://raw.githubusercontent.com/leoch627/relay-panel/main/panel_install.sh -o panel_install.sh\n` +
          `PANEL_SLAVE=1 PANEL_MASTER_URL=${master} PANEL_NODE_KEY=${d.node.key}${tlsEnv} bash panel_install.sh\n` +
          (tlsEnv ? `\n# 主控已启用 TLS（自签），PANEL_TLS_FP 为证书指纹固定，防中间人。\n` : `\n# 注意：主控未启用 TLS。\n`) +
          `\n# 2) 安装完成后节点数秒内上线，在「新建转发」里选择该节点即可。`,
      });
    } catch (e: any) { toast.error(e.message); }
  };

  const regen = async (n: NodeInfo) => {
    if (!confirm(`重置 [${n.name}] 的密钥？旧密钥立即失效，节点保持现有转发不变，需更新其配置后重启服务。`)) return;
    try {
      const d = await api<{ key: string }>(`/api/nodes/${n.id}/key`, { method: "POST" });
      setCmd({ title: `新密钥 · ${n.name}`, text: d.key });
      load();
    } catch (e: any) { toast.error(e.message); }
  };

  const del = async (n: NodeInfo) => {
    if (!confirm(`确认删除节点 [${n.name}]？\n其名下 ${n.rule_count} 条转发规则会一并删除。\n若节点仍在线，它会在下次轮询时自动清理本机实例。`)) return;
    try { await api(`/api/nodes/${n.id}`, { method: "DELETE" }); toast.success(`已删除节点 ${n.name}`); load(); }
    catch (e: any) { toast.error(e.message); }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-xl font-semibold tracking-tight">节点管理</h2>
          <p className="mt-1 text-xs text-muted-foreground">主控统一下发转发配置；被控节点出站轮询（约 5 秒），无需开放入站端口，可在 NAT 后面</p>
        </div>
        <Button size="sm" onClick={() => setAddOpen(true)}><Plus className="h-4 w-4" />添加节点</Button>
      </div>

      <Card className="overflow-hidden pb-0">
        <Table>
          <TableHeader><TableRow>
            <TableHead>节点</TableHead><TableHead>状态</TableHead><TableHead>转发数</TableHead>
            <TableHead>机器信息</TableHead><TableHead>tcp_pool</TableHead><TableHead>最近心跳</TableHead><TableHead>操作</TableHead>
          </TableRow></TableHeader>
          <TableBody>
            {nodes === null && <TableRow><TableCell colSpan={7}><div className="py-10 text-center text-sm text-muted-foreground">加载中…</div></TableCell></TableRow>}
            {nodes?.length === 0 && (
              <TableRow><TableCell colSpan={7}><div className="py-10 text-center text-sm text-muted-foreground">还没有节点。点击「添加节点」生成密钥后，在被控机上执行安装命令即可接管。</div></TableCell></TableRow>
            )}
            {nodes?.map((n) => {
              const m = n.report?.machine || {};
              return (
                <TableRow key={n.id}>
                  <TableCell>
                    <div className="font-medium">{n.name}</div>
                    <div className="font-mono text-[11px] text-muted-foreground">#{n.id}</div>
                  </TableCell>
                  <TableCell>{n.online ? <Badge variant="success">在线</Badge> : <Badge variant="destructive">离线</Badge>}</TableCell>
                  <TableCell>{n.rule_count}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {m.os || "-"} · {m.cpu_cores ?? "?"} 核 · {m.mem_total ? fmtBytes(m.mem_total) : "-"}
                  </TableCell>
                  <TableCell className="font-mono text-[11px] text-muted-foreground">{(n.report?.tcp_pool || "未上报").split("(")[0]}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{n.last_seen ? n.last_seen.replace("T", " ").slice(0, 19) : "从未"}</TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1.5">
                      <Button size="sm" variant="secondary" onClick={() => setCmd({ title: `被控安装命令 · ${n.name}`, text: slaveCmd(n) })}>
                        <Terminal className="h-3.5 w-3.5" />安装命令
                      </Button>
                      <Button size="sm" variant="secondary" onClick={() => setCmd({ title: `节点密钥 · ${n.name}`, text: n.key || "（仅管理员可见）" })}>
                        <KeyRound className="h-3.5 w-3.5" />显示密钥
                      </Button>
                      <Button size="sm" variant="secondary" onClick={() => regen(n)}><RotateCcw className="h-3.5 w-3.5" />重置密钥</Button>
                      <Button size="sm" variant="destructive" onClick={() => del(n)}>删除</Button>
                    </div>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </Card>

      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader><DialogTitle>添加节点</DialogTitle></DialogHeader>
          <div className="space-y-2">
            <Label>节点名称（如 HK-line1）</Label>
            <Input value={name} placeholder="1-64 字符" onChange={(e) => setName(e.target.value)} />
          </div>
          <DialogFooter>
            <Button variant="secondary" onClick={() => setAddOpen(false)}>取消</Button>
            <Button onClick={addNode}>创建并生成密钥</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={!!cmd} onOpenChange={() => setCmd(null)}>
        <DialogContent className="max-w-2xl">
          <DialogHeader><DialogTitle>{cmd?.title}</DialogTitle></DialogHeader>
          <pre className="max-h-[60vh] overflow-auto whitespace-pre-wrap break-all rounded-md border bg-background p-3 font-mono text-xs leading-relaxed text-muted-foreground">{cmd?.text}</pre>
          <DialogFooter>
            <Button variant="secondary" onClick={async () => {
              try { await navigator.clipboard.writeText(cmd?.text || ""); toast.success("已复制到剪贴板"); }
              catch { toast.error("复制失败，请手动选择文本"); }
            }}><Copy className="h-4 w-4" />复制</Button>
            <Button onClick={() => setCmd(null)}>关闭</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
