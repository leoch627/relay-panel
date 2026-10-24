import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api, ADV, statusBadgeVariant, type NodeInfo, type Rule } from "@/api";
import { fmtBytes } from "@/lib/utils";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, DialogDescription } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { TrafficBar } from "@/components/ui/progress";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { FileDown, FileUp, Plus, ListPlus, ScrollText, ChevronDown, ChevronRight } from "lucide-react";

interface RuleForm {
  tag: string; remark: string; listen_ip: string; listen_port: string;
  remote_host: string; remote_tcp_port: string; remote_udp_port: string;
  enable_tcp: boolean; enable_udp: boolean; node_id: string;
  rate_limit_kbps: string; traffic_quota_gb: string; advanced: Record<string, string>;
}

const emptyForm: RuleForm = {
  tag: "", remark: "", listen_ip: "0.0.0.0", listen_port: "", remote_host: "",
  remote_tcp_port: "", remote_udp_port: "", enable_tcp: true, enable_udp: true,
  node_id: "0", rate_limit_kbps: "", traffic_quota_gb: "", advanced: {},
};

function RuleDialog({ open, onClose, onSaved, editing, isAdmin }: {
  open: boolean; onClose: () => void; onSaved: () => void;
  editing: Rule | null; isAdmin: boolean;
}) {
  const [f, setF] = useState<RuleForm>(emptyForm);
  const [nodes, setNodes] = useState<NodeInfo[]>([]);
  const [advOpen, setAdvOpen] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    setAdvOpen(false);
    if (editing) {
      setF({
        tag: editing.tag, remark: editing.remark, listen_ip: editing.listen_ip,
        listen_port: String(editing.listen_port), remote_host: editing.remote_host,
        remote_tcp_port: String(editing.remote_tcp_port), remote_udp_port: String(editing.remote_udp_port),
        enable_tcp: editing.enable_tcp, enable_udp: editing.enable_udp,
        node_id: String(editing.node_id),
        rate_limit_kbps: editing.rate_limit_kbps ? String(editing.rate_limit_kbps) : "",
        traffic_quota_gb: editing.traffic_quota_bytes ? (editing.traffic_quota_bytes / 1073741824).toFixed(1) : "",
        advanced: Object.fromEntries(Object.entries(editing.advanced || {}).map(([k, v]) => [k, String(v)])),
      });
    } else {
      setF({ ...emptyForm });
    }
    api<{ nodes: NodeInfo[] }>("/api/nodes").then((d) => setNodes(d.nodes)).catch(() => {});
  }, [open, editing]);

  const set = (k: keyof RuleForm, v: any) => setF((p) => ({ ...p, [k]: v }));

  const save = async () => {
    const body = {
      tag: f.tag.trim(), remark: f.remark.trim(), listen_ip: f.listen_ip.trim(),
      listen_port: parseInt(f.listen_port, 10),
      remote_host: f.remote_host.trim(),
      remote_tcp_port: parseInt(f.remote_tcp_port, 10),
      remote_udp_port: parseInt(f.remote_udp_port, 10) || 0,
      enable_tcp: f.enable_tcp, enable_udp: f.enable_udp,
      node_id: parseInt(f.node_id, 10) || 0,
      rate_limit_kbps: parseInt(f.rate_limit_kbps, 10) || 0,
      traffic_quota_bytes: Math.round((parseFloat(f.traffic_quota_gb) || 0) * 1073741824),
      advanced: Object.fromEntries(
        Object.entries(f.advanced).filter(([, v]) => v !== "").map(([k, v]) => [k, parseInt(v, 10)])
      ),
    };
    if (!body.enable_tcp && !body.enable_udp) { toast.error("TCP / UDP 至少启用一个"); return; }
    setSaving(true);
    try {
      await api(editing ? `/api/rules/${encodeURIComponent(body.tag)}` : "/api/rules", {
        method: editing ? "PUT" : "POST", body,
      });
      toast.success(editing ? `已保存并重启 ${body.tag}` : `已创建 ${body.tag}`);
      onSaved(); onClose();
    } catch (e: any) { toast.error(e.message); } finally { setSaving(false); }
  };

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{editing ? "编辑转发" : "新建转发"}</DialogTitle>
          <DialogDescription>实例名 tcp-pool@标签；远端规则约 5 秒内经节点轮询生效</DialogDescription>
        </DialogHeader>
        <div className="grid grid-cols-2 gap-4 max-md:grid-cols-1">
          <div className="space-y-2">
            <Label>标签</Label>
            <Input value={f.tag} disabled={!!editing} placeholder="如 HK-JP-01" onChange={(e) => set("tag", e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label>运行节点</Label>
            <Select value={f.node_id} onValueChange={(v) => set("node_id", v)}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="0">本机（主控服务器）</SelectItem>
                {nodes.map((n) => <SelectItem key={n.id} value={String(n.id)}>{n.name}{n.online ? "（在线）" : "（离线）"}</SelectItem>)}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label>备注（可选）</Label>
            <Input value={f.remark} placeholder="如 电信优化线路" onChange={(e) => set("remark", e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label>监听 IP（所选节点网卡地址）</Label>
            <Input value={f.listen_ip} placeholder="0.0.0.0 / ::" onChange={(e) => set("listen_ip", e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label>监听端口</Label>
            <Input type="number" value={f.listen_port} placeholder="如 20000" onChange={(e) => set("listen_port", e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label>远端地址（落地机 IP 或域名）</Label>
            <Input value={f.remote_host} placeholder="如 1.2.3.4" onChange={(e) => set("remote_host", e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label>远端 TCP 端口</Label>
            <Input type="number" value={f.remote_tcp_port} placeholder="如 443" onChange={(e) => set("remote_tcp_port", e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label>远端 UDP 端口（留空同 TCP）</Label>
            <Input type="number" value={f.remote_udp_port} placeholder="同 TCP" onChange={(e) => set("remote_udp_port", e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label>限速 KB/s（0 = 不限）</Label>
            <Input type="number" value={f.rate_limit_kbps} placeholder="如 10240" onChange={(e) => set("rate_limit_kbps", e.target.value)} />
          </div>
          <div className="space-y-2">
            <Label>流量配额 GB（0 = 不限）</Label>
            <Input type="number" step="0.1" value={f.traffic_quota_gb} placeholder="如 100" onChange={(e) => set("traffic_quota_gb", e.target.value)} />
          </div>
        </div>
        <div className="flex items-center gap-6 pt-1">
          <label className="flex items-center gap-2 text-sm">
            <Switch checked={f.enable_tcp} onCheckedChange={(v) => set("enable_tcp", v)} />TCP
          </label>
          <label className="flex items-center gap-2 text-sm">
            <Switch checked={f.enable_udp} onCheckedChange={(v) => set("enable_udp", v)} />UDP
          </label>
        </div>
        <button
          className="flex items-center gap-1 text-xs text-primary hover:underline"
          onClick={() => setAdvOpen(!advOpen)}
        >
          {advOpen ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
          高级参数（可选，默认使用程序内置值）
        </button>
        {advOpen && (
          <div className="grid grid-cols-[150px_110px_1fr] items-center gap-2.5 max-md:grid-cols-[1fr_100px]">
            {ADV.map(([k, d, def, range]) => (
              <div key={k} className="contents">
                <div className="font-mono text-xs">{k}</div>
                <Input
                  className="h-8"
                  type="number"
                  placeholder={String(def)}
                  value={f.advanced[k] ?? ""}
                  onChange={(e) => set("advanced", { ...f.advanced, [k]: e.target.value })}
                />
                <div className="text-[11px] text-muted-foreground max-md:hidden">{d} · {range}</div>
              </div>
            ))}
          </div>
        )}
        <DialogFooter>
          <Button variant="secondary" onClick={onClose}>取消</Button>
          <Button onClick={save} disabled={saving}>{editing ? "保存并重启实例" : "创建并启动"}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function LogsDialog({ rule, onClose }: { rule: Rule; onClose: () => void }) {
  const [logs, setLogs] = useState("加载中…");
  const load = useCallback(() => {
    api<{ logs: string }>(`/api/rules/${encodeURIComponent(rule.tag)}/logs?lines=300`)
      .then((d) => setLogs(d.logs || "（无日志）"))
      .catch((e) => setLogs("读取失败: " + e.message));
  }, [rule.tag]);
  useEffect(load, [load]);
  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-w-3xl">
        <DialogHeader><DialogTitle>日志 · {rule.tag}</DialogTitle></DialogHeader>
        <pre className="max-h-[60vh] overflow-auto whitespace-pre-wrap break-all rounded-md border bg-background p-3 font-mono text-xs leading-relaxed text-muted-foreground">{logs}</pre>
        <DialogFooter>
          <Button variant="secondary" onClick={load}>刷新</Button>
          <Button onClick={onClose}>关闭</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function Rules({ isAdmin }: { isAdmin: boolean }) {
  const [rules, setRules] = useState<Rule[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editing, setEditing] = useState<Rule | null>(null);
  const [logsRule, setLogsRule] = useState<Rule | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [impOpen, setImpOpen] = useState(false);
  const [impJson, setImpJson] = useState("");
  const [batchOpen, setBatchOpen] = useState(false);
  const [batchText, setBatchText] = useState("");
  const [batchNode, setBatchNode] = useState("0");
  const [nodes, setNodes] = useState<NodeInfo[]>([]);

  const load = useCallback(() => {
    api<{ rules: Rule[] }>("/api/rules").then((d) => { setRules(d.rules || []); setLoaded(true); }).catch(() => {});
  }, []);
  useEffect(() => { load(); }, [load]);
  useEffect(() => {
    if (isAdmin) api<{ nodes: NodeInfo[] }>("/api/nodes").then((d) => setNodes(d.nodes)).catch(() => {});
  }, [isAdmin]);

  const toggleSel = (tag: string) => setSelected((p) => {
    const n = new Set(p); n.has(tag) ? n.delete(tag) : n.add(tag); return n;
  });
  const allSel = rules.length > 0 && rules.every((r) => selected.has(r.tag));
  const toggleAll = () => setSelected(allSel ? new Set() : new Set(rules.map((r) => r.tag)));

  const batch = async (action: "start" | "stop" | "delete") => {
    if (!selected.size) return;
    if (action === "delete" && !confirm(`确认批量删除 ${selected.size} 条转发？`)) return;
    try {
      const res = await api<{ done: number; skipped: number }>("/api/rules/batch", {
        body: { tags: [...selected], action },
      });
      toast.success(`批量操作完成：${res.done} 成功${res.skipped ? `，${res.skipped} 跳过` : ""}`);
      setSelected(new Set()); load();
    } catch (e: any) { toast.error(e.message); }
  };

  const doAction = async (r: Rule, action: "start" | "stop") => {
    try {
      await api(`/api/rules/${encodeURIComponent(r.tag)}/${action}`, { method: "POST" });
      toast.success(action === "start" ? `已下发启动指令：${r.tag}` : `已下发停止指令：${r.tag}`);
      load();
    } catch (e: any) { toast.error(e.message); }
  };

  const del = async (r: Rule) => {
    const where = r.node_id === 0 ? "" : "\n（远端规则：节点下次轮询时自动清理实例）";
    if (!confirm(`确认删除转发 [${r.tag}]？\n监听 ${r.listen_ip}:${r.listen_port} → ${r.remote_host}${where}`)) return;
    try { await api(`/api/rules/${encodeURIComponent(r.tag)}`, { method: "DELETE" }); toast.success(`已删除 ${r.tag}`); load(); }
    catch (e: any) { toast.error(e.message); }
  };

  // 批量添加：每行 [tag:]监听端口 远端地址 [远端端口] [tcp|udp|both]
  const parseBatch = (): { rules: any[]; errors: string[] } => {
    const rules: any[] = [];
    const errors: string[] = [];
    let auto = 1;
    batchText.split("\n").forEach((raw, i) => {
      const line = raw.trim();
      if (!line || line.startsWith("#")) return;
      const parts = line.split(/\s+/);
      let tag = "", portPart = parts[0];
      if (portPart.includes(":")) {
        const seg = portPart.split(":");
        tag = seg[0]; portPart = seg[1];
      }
      const listenPort = parseInt(portPart, 10);
      const host = parts[1];
      let rport = parts[2] ? parseInt(parts[2], 10) : NaN;
      let proto: string | undefined = parts[3];
      if (!host) { errors.push(`第 ${i + 1} 行缺少远端地址`); return; }
      if (!listenPort || listenPort < 1 || listenPort > 65535) { errors.push(`第 ${i + 1} 行监听端口不合法`); return; }
      if (!rport || rport < 1 || rport > 65535) rport = listenPort;
      const both = !proto || proto === "both";
      rules.push({
        tag: tag || `B${auto++}`,
        listen_ip: "0.0.0.0", listen_port: listenPort,
        remote_host: host, remote_tcp_port: rport, remote_udp_port: rport,
        enable_tcp: both || proto === "tcp", enable_udp: both || proto === "udp",
        node_id: parseInt(batchNode, 10) || 0, advanced: {},
      });
    });
    return { rules, errors };
  };

  const submitBatch = async () => {
    const { rules, errors } = parseBatch();
    if (!rules.length) { toast.error(errors[0] || "没有可添加的行"); return; }
    try {
      const res = await api<{ imported: number; skipped: number; failed: number; error: string }>("/api/rules/import", {
        body: { rules, node_id: parseInt(batchNode, 10) || 0 },
      });
      const errLines = errors.length ? `；${errors.length} 行格式错误被忽略` : "";
      toast[res.failed ? "error" : "success"](`批量添加：${res.imported} 成功，${res.skipped} 跳过，${res.failed} 失败${res.error ? `（${res.error}）` : ""}${errLines}`);
      if (res.imported > 0) { setBatchOpen(false); setBatchText(""); load(); }
    } catch (e: any) { toast.error(e.message); }
  };

  const exportRules = async () => {
    const d = await api<{ rules: any[] }>("/api/rules/export");
    setImpJson(JSON.stringify(d.rules, null, 2)); setImpOpen(true);
  };

  const importRules = async () => {
    let rules;
    try { rules = JSON.parse(impJson); } catch { toast.error("JSON 解析失败"); return; }
    if (!Array.isArray(rules)) { toast.error("需要 JSON 数组"); return; }
    try {
      const res = await api<{ imported: number; skipped: number; failed: number; error: string }>("/api/rules/import", { body: { rules } });
      toast[res.failed ? "error" : "success"](`导入：${res.imported} 成功，${res.skipped} 跳过，${res.failed} 失败${res.error ? `（${res.error}）` : ""}`);
      setImpOpen(false); load();
    } catch (e: any) { toast.error(e.message); }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-xl font-semibold tracking-tight">转发管理</h2>
          <p className="mt-1 text-xs text-muted-foreground">共 {rules.length} 条转发；远端规则的变更在节点下次轮询（约 5 秒内）生效</p>
        </div>
        <div className="flex gap-2">
          {isAdmin && <>
            <Button variant="secondary" size="sm" onClick={() => setBatchOpen(true)}><ListPlus className="h-4 w-4" />批量添加</Button>
            <Button variant="secondary" size="sm" onClick={exportRules}><FileDown className="h-4 w-4" />导出</Button>
            <Button variant="secondary" size="sm" onClick={() => { setImpJson(""); setImpOpen(true); }}><FileUp className="h-4 w-4" />导入</Button>
          </>}
          <Button size="sm" onClick={() => { setEditing(null); setDialogOpen(true); }}><Plus className="h-4 w-4" />新建转发</Button>
        </div>
      </div>

      {selected.size > 0 && (
        <Card className="flex flex-wrap items-center gap-3 p-3">
          <b className="text-sm">{selected.size}</b><span className="text-xs text-muted-foreground">条已选</span>
          <Button size="sm" onClick={() => batch("start")}>批量启动</Button>
          <Button size="sm" variant="secondary" onClick={() => batch("stop")}>批量停止</Button>
          <Button size="sm" variant="destructive" onClick={() => batch("delete")}>批量删除</Button>
          <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>取消选择</Button>
        </Card>
      )}

      <Card className="overflow-hidden pb-0">
        <Table>
          <TableHeader><TableRow>
            <TableHead className="w-9"><input type="checkbox" checked={allSel} onChange={toggleAll} className="accent-primary" /></TableHead>
            <TableHead>标签</TableHead><TableHead>节点</TableHead><TableHead>监听</TableHead><TableHead>转发到</TableHead>
            <TableHead>协议</TableHead><TableHead>探测</TableHead><TableHead>流量 / 配额</TableHead>
            {isAdmin && <TableHead>归属</TableHead>}
            <TableHead>状态</TableHead><TableHead>期望</TableHead><TableHead>操作</TableHead>
          </TableRow></TableHeader>
          <TableBody>
            {!loaded && <TableRow><TableCell colSpan={12}><div className="py-10 text-center text-sm text-muted-foreground">加载中…</div></TableCell></TableRow>}
            {loaded && rules.length === 0 && (
              <TableRow><TableCell colSpan={12}><div className="py-10 text-center text-sm text-muted-foreground">还没有转发，点击右上角「新建转发」创建第一条</div></TableCell></TableRow>
            )}
            {rules.map((r) => {
              const sb = statusBadgeVariant(r.status.active);
              const used = (r.stats?.up || 0) + (r.stats?.down || 0);
              const quota = r.traffic_quota_bytes || 0;
              const pct = quota ? (used / quota) * 100 : 0;
              const desired = r.desired_state || "running";
              return (
                <TableRow key={r.tag}>
                  <TableCell><input type="checkbox" checked={selected.has(r.tag)} onChange={() => toggleSel(r.tag)} className="accent-primary" /></TableCell>
                  <TableCell>
                    <div className="font-medium">{r.tag}</div>
                    {r.remark && <div className="text-[11px] text-muted-foreground">{r.remark}</div>}
                  </TableCell>
                  <TableCell>{r.node_id === 0 ? <Badge variant="secondary">本机</Badge> : <Badge variant="info">{r.node_name}</Badge>}</TableCell>
                  <TableCell className="font-mono text-xs">{r.listen_ip}:{r.listen_port}</TableCell>
                  <TableCell className="font-mono text-xs">
                    {r.remote_host}
                    <div className="text-[11px] text-muted-foreground">tcp:{r.remote_tcp_port} / udp:{r.remote_udp_port}</div>
                  </TableCell>
                  <TableCell className="whitespace-nowrap">
                    {r.enable_tcp && <Badge variant="info" className="mr-1">TCP</Badge>}
                    {r.enable_udp && <Badge variant="warning">UDP</Badge>}
                  </TableCell>
                  <TableCell className="font-mono text-xs">
                    {r.probe ? (
                      r.probe.ok
                        ? <span className="text-emerald-400">{r.probe.ms}ms</span>
                        : <span className={r.probe.soft ? "text-amber-400" : "text-red-400"} title={r.probe.err}>{r.probe.soft ? "无响应" : "超时"}</span>
                    ) : <span className="text-muted-foreground">-</span>}
                  </TableCell>
                  <TableCell>
                    {quota ? (
                      <>
                        <TrafficBar pct={pct} />
                        <div className="mt-1 text-[11px] text-muted-foreground">
                          {fmtBytes(used)} / {fmtBytes(quota)}
                          {(r.stats?.over_quota || r.over_quota) && <Badge variant="destructive" className="ml-1">超限</Badge>}
                        </div>
                      </>
                    ) : (
                      <div className="text-[11px] text-muted-foreground">
                        {fmtBytes(used)}{r.rate_limit_kbps ? ` · 限速 ${r.rate_limit_kbps}KB/s` : ""}
                      </div>
                    )}
                  </TableCell>
                  {isAdmin && <TableCell className="text-xs text-muted-foreground">{r.owner}{r.my_rule ? " ·我" : ""}</TableCell>}
                  <TableCell><Badge variant={sb.variant as any}>{sb.text}</Badge></TableCell>
                  <TableCell>{desired === "running" ? <Badge variant="success">运行</Badge> : <Badge variant="secondary">停止</Badge>}</TableCell>
                  <TableCell>
                    <div className="flex flex-wrap gap-1.5">
                      <Button size="sm" variant="secondary" onClick={() => doAction(r, desired === "running" ? "stop" : "start")}>
                        {desired === "running" ? "停止" : "启动"}
                      </Button>
                      {r.node_id === 0 && (
                        <Button size="sm" variant="secondary" onClick={() => setLogsRule(r)}><ScrollText className="h-3.5 w-3.5" />日志</Button>
                      )}
                      <Button size="sm" variant="secondary" onClick={() => { setEditing(r); setDialogOpen(true); }}>编辑</Button>
                      <Button size="sm" variant="destructive" onClick={() => del(r)}>删除</Button>
                    </div>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </Card>

      <RuleDialog open={dialogOpen} onClose={() => setDialogOpen(false)} onSaved={load} editing={editing} isAdmin={isAdmin} />
      {logsRule && <LogsDialog rule={logsRule} onClose={() => setLogsRule(null)} />}

      <Dialog open={batchOpen} onOpenChange={setBatchOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>批量添加转发</DialogTitle>
            <DialogDescription>
              每行一条：<code className="rounded bg-muted px-1 font-mono text-xs">[标签:]监听端口 远端地址 [远端端口] [tcp|udp|both]</code>；
              远端端口缺省同监听端口，协议缺省 TCP+UDP；标签缺省自动编号
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label>目标节点</Label>
            <Select value={batchNode} onValueChange={setBatchNode}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="0">本机（主控服务器）</SelectItem>
                {nodes.map((n) => <SelectItem key={n.id} value={String(n.id)}>{n.name}{n.online ? "（在线）" : "（离线）"}</SelectItem>)}
              </SelectContent>
            </Select>
          </div>
          <textarea
            value={batchText}
            onChange={(e) => setBatchText(e.target.value)}
            placeholder={"HK1:20001 1.2.3.4 443\n20002 us.example.com 8080 tcp\n20003 1.1.1.1\n# 支持注释行"}
            className="h-48 w-full rounded-md border border-input bg-background p-2.5 font-mono text-xs outline-none focus:ring-2 focus:ring-ring"
          />
          {batchText.trim() && (
            <div className="text-xs text-muted-foreground">
              解析结果：{parseBatch().rules.length} 条{parseBatch().errors.length ? `，${parseBatch().errors.length} 行有误` : ""}
            </div>
          )}
          <DialogFooter>
            <Button variant="secondary" onClick={() => setBatchOpen(false)}>取消</Button>
            <Button onClick={submitBatch}>添加</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={impOpen} onOpenChange={setImpOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>导入转发规则</DialogTitle>
            <DialogDescription>粘贴「导出」得到的 JSON 规则数组；已存在的标签跳过，导入规则一律落在本机</DialogDescription>
          </DialogHeader>
          <textarea
            value={impJson}
            onChange={(e) => setImpJson(e.target.value)}
            placeholder='[{"tag":"HK01",...}]'
            className="h-64 w-full rounded-md border border-input bg-background p-2.5 font-mono text-xs outline-none focus:ring-2 focus:ring-ring"
          />
          <DialogFooter>
            <Button variant="secondary" onClick={() => setImpOpen(false)}>取消</Button>
            <Button onClick={importRules}>导入</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
