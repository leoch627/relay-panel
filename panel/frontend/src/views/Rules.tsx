import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import {
  ArrowRight, ChevronDown, Copy, FileDown, FileUp, ListPlus, MoreHorizontal, Pencil, Play, Plus, ScrollText, Search,
  Server, Square, Trash2, X, ArrowLeftRight,
} from "lucide-react";
import { api, errMsg, type NodeInfo, type Rule } from "@/api";
import { cn, escTime, fmtBytes, fmtRate } from "@/lib/utils";
import { usePolling } from "@/lib/usePolling";
import { ruleDestinations } from "@/lib/ruleDestinations";
import { isHalted, probeView, ruleState, toneText } from "@/lib/ruleState";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Meter, Skeleton } from "@/components/ui/misc";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Tooltip } from "@/components/ui/tooltip";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { EmptyState, ErrorBanner, PageHeader, StatusDot, StatusLabel, nodeTone } from "@/components/common";
import { confirm } from "@/components/confirm";
import { RuleSheet, type SheetMode } from "@/views/rules/RuleSheet";
import { BatchDialog, ImportDialog, LogsDialog } from "@/views/rules/dialogs";

type StatusFilter = "all" | "running" | "stopped" | "issue";

const overQuota = (r: Rule) => r.over_quota || !!r.stats?.over_quota;

function NodeCell({ rule, node }: { rule: Rule; node?: NodeInfo }) {
  if (rule.node_id === 0) {
    return <span className="inline-flex items-center gap-1.5 text-[13px]"><Server className="h-3.5 w-3.5 text-primary" />本机</span>;
  }
  return (
    <span className="inline-flex max-w-40 items-center gap-2 text-[13px]">
      {node ? <StatusDot tone={nodeTone(node)} /> : <StatusDot tone="secondary" />}
      <span className="truncate">{rule.node_name}</span>
    </span>
  );
}

function RouteCell({ rule }: { rule: Rule }) {
  const wildcard = rule.listen_ip === "0.0.0.0" || rule.listen_ip === "::";
  return (
    <div className="flex items-center gap-2.5 font-mono text-xs">
      <span className="whitespace-nowrap rounded-md bg-muted px-1.5 py-0.5 text-foreground">{wildcard ? "" : rule.listen_ip}:{rule.listen_port}</span>
      <ArrowRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <div className="min-w-0 space-y-1">
        {ruleDestinations(rule).map((d) => (
          <div key={d.key} className="flex items-center gap-1.5">
            <span className="max-w-[14rem] truncate">{d.host}:{d.port}</span>
            {d.protocols.map((p) => <Badge key={p} variant={p === "TCP" ? "info" : "warning"} className="px-1 py-0 font-sans text-[10px]">{p}</Badge>)}
          </div>
        ))}
      </div>
    </div>
  );
}

function TrafficCell({ rule }: { rule: Rule }) {
  const used = (rule.stats?.up || 0) + (rule.stats?.down || 0);
  const quota = rule.traffic_quota_bytes || 0;
  return (
    <div className="w-32 space-y-1.5">
      <div className="tabular flex items-baseline justify-between gap-2 text-xs">
        <span className="font-medium">{fmtBytes(used)}</span>
        {quota > 0 && <span className="text-muted-foreground">/ {fmtBytes(quota)}</span>}
      </div>
      {quota > 0 && <Meter value={(used / quota) * 100} />}
      {rule.rate_limit_kbps > 0 && <div className="text-[11px] text-muted-foreground">限速 {fmtRate(rule.rate_limit_kbps)}</div>}
    </div>
  );
}

export function Rules({ isAdmin }: { isAdmin: boolean }) {
  const [sheet, setSheet] = useState<{ open: boolean; mode: SheetMode; rule: Rule | null }>({ open: false, mode: "create", rule: null });
  const [logsRule, setLogsRule] = useState<Rule | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [batchOpen, setBatchOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [q, setQ] = useState("");
  const [status, setStatus] = useState<StatusFilter>("all");
  const [nodeFilter, setNodeFilter] = useState("all");
  const [busy, setBusy] = useState(false);

  const request = useCallback(async (signal: AbortSignal) => {
    const [ruleData, nodeData] = await Promise.all([
      api<{ rules: Rule[] }>("/api/rules", { signal }),
      api<{ nodes: NodeInfo[] }>("/api/nodes", { signal }),
    ]);
    return { rules: ruleData.rules || [], nodes: nodeData.nodes || [] };
  }, []);
  const { data, error, reload } = usePolling(request);
  const rules = useMemo(() => data?.rules || [], [data]);
  const nodes = useMemo(() => data?.nodes || [], [data]);
  const nodeMap = useMemo(() => new Map(nodes.map((n) => [n.id, n])), [nodes]);

  // 列表刷新后剔除已不存在的选中项
  useEffect(() => {
    setSelected((prev) => {
      const tags = new Set(rules.map((r) => r.tag));
      const next = new Set([...prev].filter((t) => tags.has(t)));
      return next.size === prev.size ? prev : next;
    });
  }, [rules]);

  const counts = useMemo(() => {
    const c = { all: rules.length, running: 0, stopped: 0, issue: 0 };
    for (const r of rules) c[ruleState(r).group]++;
    return c;
  }, [rules]);

  const filtered = useMemo(() => {
    const s = q.trim().toLowerCase();
    return rules.filter((r) => {
      if (status !== "all" && ruleState(r).group !== status) return false;
      if (nodeFilter !== "all" && String(r.node_id) !== nodeFilter) return false;
      if (!s) return true;
      return [r.tag, r.remark, r.remote_host, String(r.listen_port), r.node_name, r.owner].some((x) => x?.toLowerCase().includes(s));
    });
  }, [rules, q, status, nodeFilter]);

  const visibleSelected = filtered.filter((r) => selected.has(r.tag)).length;
  const headerChecked = filtered.length > 0 && visibleSelected === filtered.length ? true : visibleSelected > 0 ? "indeterminate" : false;
  const toggleAll = () => setSelected((prev) => {
    const next = new Set(prev);
    if (headerChecked === true) filtered.forEach((r) => next.delete(r.tag));
    else filtered.forEach((r) => next.add(r.tag));
    return next;
  });
  const toggleOne = (tag: string) => setSelected((prev) => {
    const next = new Set(prev);
    if (next.has(tag)) next.delete(tag); else next.add(tag);
    return next;
  });

  const runBatch = async (action: "start" | "stop" | "delete") => {
    if (!selected.size) return;
    if (action === "delete" && !(await confirm({ title: `删除 ${selected.size} 条转发？`, description: "本机实例会立即停止；远端规则在节点下次轮询时清理。此操作不可撤销。", confirmText: "删除", destructive: true }))) return;
    if (action === "start" && rules.some((r) => selected.has(r.tag) && overQuota(r)) &&
      !(await confirm({ title: "包含已超配额的转发", description: "重新启动会清零这些实例的用量并重新计算配额，确认继续？", confirmText: "继续启动" }))) return;
    setBusy(true);
    try {
      const res = await api<{ done: number; skipped: number; error?: string }>("/api/rules/batch", { body: { tags: [...selected], action } });
      toast[res.error ? "warning" : "success"](`批量操作完成：${res.done} 成功${res.skipped ? `，${res.skipped} 跳过` : ""}`, { description: res.error || undefined });
      setSelected(new Set()); reload();
    } catch (e) { toast.error(errMsg(e)); } finally { setBusy(false); }
  };

  const toggleRule = async (r: Rule) => {
    const action = isHalted(r) ? "start" : "stop";
    if (action === "start" && overQuota(r) &&
      !(await confirm({ title: `重新启动 ${r.tag}？`, description: "该转发已达流量配额。重新启动会清零实例用量并重新计算配额。", confirmText: "重新启动" }))) return;
    try {
      await api(`/api/rules/${encodeURIComponent(r.tag)}/${action}`, { method: "POST" });
      toast.success(action === "start" ? `已下发启动：${r.tag}` : `已下发停止：${r.tag}`);
      reload();
    } catch (e) { toast.error(errMsg(e)); }
  };

  const del = async (r: Rule) => {
    const where = r.node_id === 0 ? "本机实例会立即停止并清理。" : "远端规则会在节点下次轮询时自动清理实例。";
    if (!(await confirm({ title: `删除转发 ${r.tag}？`, description: `监听 ${r.listen_ip}:${r.listen_port} → ${r.remote_host}\n${where}`, confirmText: "删除", destructive: true }))) return;
    try { await api(`/api/rules/${encodeURIComponent(r.tag)}`, { method: "DELETE" }); toast.success(`已删除 ${r.tag}`); reload(); }
    catch (e) { toast.error(errMsg(e)); }
  };

  const exportRules = async () => {
    try {
      const d = await api<{ rules: unknown[] }>("/api/rules/export");
      const blob = new Blob([JSON.stringify(d.rules, null, 2)], { type: "application/json" });
      const a = document.createElement("a");
      const stamp = escTime(new Date().toISOString()).slice(0, 16).replace(/\D/g, "");
      a.href = URL.createObjectURL(blob);
      a.download = `relay-rules-${stamp}.json`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(a.href), 1000);
      toast.success(`已导出 ${d.rules.length} 条规则`);
    } catch (e) { toast.error(errMsg(e)); }
  };

  const openSheet = (mode: SheetMode, rule: Rule | null = null) => setSheet({ open: true, mode, rule });
  const colSpan = isAdmin ? 9 : 8;

  return (
    <div>
      <PageHeader
        title="转发规则"
        description="管理所有 TCP / UDP 转发实例。远端节点上的变更会在其下次轮询（约 5 秒）时生效。"
        actions={<>
          {isAdmin && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="outline" size="sm">更多操作<ChevronDown className="opacity-60" /></Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-44">
                <DropdownMenuItem onSelect={() => setBatchOpen(true)}><ListPlus />批量添加</DropdownMenuItem>
                <DropdownMenuItem onSelect={() => setImportOpen(true)}><FileUp />导入 JSON</DropdownMenuItem>
                <DropdownMenuItem onSelect={exportRules}><FileDown />导出 JSON</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          <Button size="sm" onClick={() => openSheet("create")}><Plus />新建转发</Button>
        </>}
      />
      <ErrorBanner message={error} />

      <Card className="overflow-hidden">
        <div className="flex flex-wrap items-center gap-3 border-b p-3">
          <Tabs value={status} onValueChange={(v) => setStatus(v as StatusFilter)}>
            <TabsList>
              {([["all", "全部"], ["running", "运行"], ["stopped", "停止"], ["issue", "异常"]] as const).map(([k, label]) => (
                <TabsTrigger key={k} value={k}>
                  {label}
                  <span className={cn("tabular rounded px-1 text-[11px]", k === "issue" && counts.issue > 0 ? "bg-destructive/10 text-destructive" : "text-muted-foreground")}>{counts[k]}</span>
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
          <div className="ml-auto flex flex-1 flex-wrap items-center justify-end gap-2">
            <Select value={nodeFilter} onValueChange={setNodeFilter}>
              <SelectTrigger className="h-9 w-full sm:w-40"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">全部节点</SelectItem>
                <SelectItem value="0">本机</SelectItem>
                {nodes.map((n) => <SelectItem key={n.id} value={String(n.id)}>{n.name}</SelectItem>)}
              </SelectContent>
            </Select>
            <div className="relative w-full sm:w-64">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
              <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索标签、备注、地址、端口…" className="pl-9 pr-8" />
              {q && <button onClick={() => setQ("")} className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-0.5 text-muted-foreground hover:text-foreground"><X className="h-3.5 w-3.5" /></button>}
            </div>
          </div>
        </div>

        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="w-10 pr-0"><Checkbox checked={headerChecked} onCheckedChange={toggleAll} aria-label="全选" /></TableHead>
              <TableHead>规则</TableHead>
              <TableHead>节点</TableHead>
              <TableHead>链路</TableHead>
              <TableHead>状态</TableHead>
              <TableHead>延迟</TableHead>
              <TableHead>流量</TableHead>
              {isAdmin && <TableHead>归属</TableHead>}
              <TableHead className="w-20 text-right">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {!data && Array.from({ length: 4 }).map((_, i) => (
              <TableRow key={i}><TableCell colSpan={colSpan}><Skeleton className="h-9" /></TableCell></TableRow>
            ))}
            {data && filtered.length === 0 && (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={colSpan}>
                  {rules.length === 0 ? (
                    <EmptyState icon={ArrowLeftRight} title="还没有转发规则" description="创建第一条转发，把中转节点的端口映射到落地机。" action={<Button size="sm" onClick={() => openSheet("create")}><Plus />新建转发</Button>} />
                  ) : (
                    <EmptyState icon={Search} title="没有匹配的转发" description="换个关键词或筛选条件试试" action={<Button variant="outline" size="sm" onClick={() => { setQ(""); setStatus("all"); setNodeFilter("all"); }}>清除筛选</Button>} />
                  )}
                </TableCell>
              </TableRow>
            )}
            {filtered.map((r) => {
              const st = ruleState(r);
              const pv = probeView(r);
              const halted = isHalted(r);
              const sel = selected.has(r.tag);
              return (
                <TableRow key={r.tag} data-state={sel ? "selected" : undefined}>
                  <TableCell className="pr-0"><Checkbox checked={sel} onCheckedChange={() => toggleOne(r.tag)} aria-label={`选择 ${r.tag}`} /></TableCell>
                  <TableCell>
                    <button className="text-left" onClick={() => openSheet("edit", r)}>
                      <div className="font-medium hover:text-primary">{r.tag}</div>
                      {r.remark && <div className="max-w-44 truncate text-xs text-muted-foreground">{r.remark}</div>}
                    </button>
                  </TableCell>
                  <TableCell><NodeCell rule={r} node={nodeMap.get(r.node_id)} /></TableCell>
                  <TableCell><RouteCell rule={r} /></TableCell>
                  <TableCell>
                    <Tooltip content={r.status.started_at ? `启动于 ${escTime(r.status.started_at)}${r.status.restarts && r.status.restarts !== "0" ? ` · 重启 ${r.status.restarts} 次` : ""}` : undefined}>
                      <span><StatusLabel tone={st.tone} live={st.live}>{st.label}</StatusLabel></span>
                    </Tooltip>
                  </TableCell>
                  <TableCell>
                    {pv ? (
                      <Tooltip content={`${r.probe?.err ? r.probe.err + " · " : ""}探测于 ${escTime(r.probe?.at)}`}>
                        <span className={cn("tabular inline-flex items-center gap-1.5 whitespace-nowrap font-mono text-xs", toneText[pv.tone])}>{pv.label}</span>
                      </Tooltip>
                    ) : <span className="text-xs text-muted-foreground">—</span>}
                  </TableCell>
                  <TableCell><TrafficCell rule={r} /></TableCell>
                  {isAdmin && (
                    <TableCell className="whitespace-nowrap text-[13px] text-muted-foreground">
                      {r.owner}{r.my_rule && <Badge variant="secondary" className="ml-1.5 px-1 py-0 text-[10px]">我</Badge>}
                    </TableCell>
                  )}
                  <TableCell>
                    <div className="flex items-center justify-end gap-1">
                      <Tooltip content={halted ? "启动" : "停止"}>
                        <Button size="icon-sm" variant="ghost" onClick={() => toggleRule(r)} className={halted ? "text-success hover:text-success" : "text-muted-foreground"}>
                          {halted ? <Play /> : <Square />}<span className="sr-only">{halted ? "启动" : "停止"}</span>
                        </Button>
                      </Tooltip>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button size="icon-sm" variant="ghost" className="text-muted-foreground"><MoreHorizontal /><span className="sr-only">更多</span></Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end" className="w-40">
                          <DropdownMenuItem onSelect={() => openSheet("edit", r)}><Pencil />编辑</DropdownMenuItem>
                          <DropdownMenuItem onSelect={() => openSheet("duplicate", r)}><Copy />复制为新规则</DropdownMenuItem>
                          {r.node_id === 0 && <DropdownMenuItem onSelect={() => setLogsRule(r)}><ScrollText />查看日志</DropdownMenuItem>}
                          <DropdownMenuSeparator />
                          <DropdownMenuItem destructive onSelect={() => del(r)}><Trash2 />删除</DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </div>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
        {data && filtered.length > 0 && (
          <div className="border-t px-4 py-2.5 text-xs text-muted-foreground">
            显示 {filtered.length} / {rules.length} 条
          </div>
        )}
      </Card>

      {selected.size > 0 && (
        <div className="pointer-events-none fixed inset-x-0 bottom-6 z-40 flex justify-center px-4 lg:pl-60">
          <div className="pointer-events-auto flex items-center gap-1 rounded-xl border bg-popover p-1.5 pl-4 shadow-2xl animate-in fade-in-0 slide-in-from-bottom-4">
            <span className="mr-2 text-[13px]"><b className="tabular">{selected.size}</b> 条已选</span>
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => runBatch("start")}><Play className="text-success" />启动</Button>
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => runBatch("stop")}><Square />停止</Button>
            <Button size="sm" variant="ghost" disabled={busy} className="text-destructive hover:bg-destructive/10 hover:text-destructive" onClick={() => runBatch("delete")}><Trash2 />删除</Button>
            <div className="mx-1 h-5 w-px bg-border" />
            <Tooltip content="取消选择">
              <Button size="icon-sm" variant="ghost" onClick={() => setSelected(new Set())}><X /><span className="sr-only">取消选择</span></Button>
            </Tooltip>
          </div>
        </div>
      )}

      <RuleSheet open={sheet.open} mode={sheet.mode} rule={sheet.rule} nodes={nodes} onClose={() => setSheet((s) => ({ ...s, open: false }))} onSaved={reload} />
      {logsRule && <LogsDialog rule={logsRule} onClose={() => setLogsRule(null)} />}
      {isAdmin && <>
        <BatchDialog open={batchOpen} onOpenChange={setBatchOpen} rules={rules} nodes={nodes} onDone={reload} />
        <ImportDialog open={importOpen} onOpenChange={setImportOpen} onDone={reload} />
      </>}
    </div>
  );
}
