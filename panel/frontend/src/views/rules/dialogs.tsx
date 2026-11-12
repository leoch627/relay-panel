import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { FileUp, Loader2, RefreshCw } from "lucide-react";
import { api, errMsg, type NodeInfo, type Rule } from "@/api";
import { parseBatchRules } from "@/lib/batchRules";
import { usePolling } from "@/lib/usePolling";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NodeSelect } from "@/components/node-select";
import { CopyButton } from "@/components/common";

type ImportResult = { imported: number; skipped: number; failed: number; error: string };

function summary(res: ImportResult) {
  return `${res.imported} 成功 · ${res.skipped} 跳过 · ${res.failed} 失败`;
}

export function BatchDialog({ open, onOpenChange, rules, nodes, onDone }: { open: boolean; onOpenChange: (v: boolean) => void; rules: Rule[]; nodes: NodeInfo[]; onDone: () => void }) {
  const [text, setText] = useState("");
  const [node, setNode] = useState("0");
  const [saving, setSaving] = useState(false);
  const parsed = useMemo(() => parseBatchRules(text, rules.map((r) => r.tag), Number(node)), [text, rules, node]);
  const deleting = nodes.some((n) => n.id === Number(node) && n.deleting);

  const submit = async () => {
    if (deleting) { toast.error("所选节点正在清理，请选择其他节点"); return; }
    if (parsed.errors.length) { toast.error(parsed.errors[0]); return; }
    if (!parsed.rules.length) { toast.error("没有可添加的行"); return; }
    setSaving(true);
    try {
      const res = await api<ImportResult>("/api/rules/import", { body: { rules: parsed.rules, node_id: Number(node) || 0 } });
      toast[res.failed ? "error" : "success"](`批量添加：${summary(res)}`, { description: res.error || undefined });
      if (res.imported > 0) { onOpenChange(false); setText(""); onDone(); }
    } catch (e) { toast.error(errMsg(e)); } finally { setSaving(false); }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>批量添加转发</DialogTitle>
          <DialogDescription>
            每行一条：<code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-foreground">[标签:]监听端口 远端地址 [远端端口] [tcp|udp|both]</code>
            <br />远端端口缺省同监听端口，协议缺省 TCP+UDP，标签缺省自动编号，# 开头为注释
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 md:grid-cols-2">
          <div className="space-y-3">
            <div className="space-y-2"><Label>目标节点</Label><NodeSelect value={node} onChange={setNode} nodes={nodes} /></div>
            <Textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              spellCheck={false}
              placeholder={"HK1:20001 1.2.3.4 443\n20002 us.example.com 8080 tcp\n20003 1.1.1.1 udp\n# 注释行会被忽略"}
              className="h-64 resize-none font-mono text-xs leading-relaxed"
            />
          </div>
          <div className="flex min-h-0 flex-col rounded-lg border">
            <div className="flex items-center justify-between border-b px-3 py-2 text-xs">
              <span className="font-medium">解析预览</span>
              <span className="flex gap-1.5">
                <Badge variant="success">{parsed.rules.length} 条有效</Badge>
                {parsed.errors.length > 0 && <Badge variant="destructive">{parsed.errors.length} 行有误</Badge>}
              </span>
            </div>
            <div className="max-h-[19rem] flex-1 overflow-y-auto">
              {!text.trim() ? <div className="p-6 text-center text-xs text-muted-foreground">在左侧输入后实时预览</div> : (
                <ul className="divide-y text-xs">
                  {parsed.errors.map((m) => <li key={m} className="bg-destructive/5 px-3 py-2 text-destructive">{m}</li>)}
                  {parsed.rules.map((r) => (
                    <li key={r.tag} className="flex items-center gap-2 px-3 py-2 font-mono">
                      <span className="w-16 truncate font-sans font-medium">{r.tag}</span>
                      <span className="text-muted-foreground">:{r.listen_port}</span>
                      <span className="text-muted-foreground">→</span>
                      <span className="min-w-0 flex-1 truncate">{r.remote_host}:{r.enable_tcp ? r.remote_tcp_port : r.remote_udp_port}</span>
                      <span className="font-sans text-[11px] text-muted-foreground">{[r.enable_tcp && "TCP", r.enable_udp && "UDP"].filter(Boolean).join("+")}</span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>取消</Button>
          <Button onClick={submit} disabled={saving || parsed.errors.length > 0 || parsed.rules.length === 0}>
            {saving && <Loader2 className="animate-spin" />}添加 {parsed.rules.length || ""} 条
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function ImportDialog({ open, onOpenChange, onDone }: { open: boolean; onOpenChange: (v: boolean) => void; onDone: () => void }) {
  const [text, setText] = useState("");
  const [saving, setSaving] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  useEffect(() => { if (open) setText(""); }, [open]);

  const readFile = async (file?: File) => {
    if (!file) return;
    setText(await file.text());
  };

  const submit = async () => {
    let data: unknown;
    try { data = JSON.parse(text); } catch { toast.error("JSON 解析失败"); return; }
    const rules = Array.isArray(data) ? data : (data as { rules?: unknown })?.rules;
    if (!Array.isArray(rules)) { toast.error("需要 JSON 规则数组"); return; }
    setSaving(true);
    try {
      const res = await api<ImportResult>("/api/rules/import", { body: { rules } });
      toast[res.failed ? "error" : "success"](`导入：${summary(res)}`, { description: res.error || undefined });
      onOpenChange(false); onDone();
    } catch (e) { toast.error(errMsg(e)); } finally { setSaving(false); }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>导入转发规则</DialogTitle>
          <DialogDescription>粘贴或选择「导出」得到的 JSON 文件。已存在的标签会跳过；每条规则保留原节点归属，对应节点必须已存在。</DialogDescription>
        </DialogHeader>
        <div
          className="space-y-2"
          onDragOver={(e) => e.preventDefault()}
          onDrop={(e) => { e.preventDefault(); void readFile(e.dataTransfer.files[0]); }}
        >
          <Textarea value={text} onChange={(e) => setText(e.target.value)} spellCheck={false} placeholder='[{"tag":"HK01","listen_port":20001,...}]' className="h-64 resize-none font-mono text-xs" />
          <div className="flex items-center justify-between text-xs text-muted-foreground">
            <span>也可以把 .json 文件拖到上方</span>
            <Button variant="ghost" size="xs" onClick={() => fileRef.current?.click()}><FileUp />选择文件</Button>
            <input ref={fileRef} type="file" accept=".json,application/json" className="hidden" onChange={(e) => { void readFile(e.target.files?.[0]); e.target.value = ""; }} />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>取消</Button>
          <Button onClick={submit} disabled={saving || !text.trim()}>{saving && <Loader2 className="animate-spin" />}导入</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function LogsDialog({ rule, onClose }: { rule: Rule; onClose: () => void }) {
  const request = useCallback((signal: AbortSignal) =>
    api<{ logs: string }>(`/api/rules/${encodeURIComponent(rule.tag)}/logs?lines=300`, { signal }), [rule.tag]);
  const { data, error, reload } = usePolling(request, null);
  const logs = error ? `读取失败：${error}` : data ? data.logs || "（暂无日志）" : "";
  const preRef = useRef<HTMLPreElement>(null);
  useEffect(() => { const el = preRef.current; if (el) el.scrollTop = el.scrollHeight; }, [logs]);

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="max-w-4xl gap-4">
        <DialogHeader>
          <DialogTitle>实例日志 · {rule.tag}</DialogTitle>
          <DialogDescription className="font-mono">journalctl -u tcp-pool@{rule.tag} -n 300</DialogDescription>
        </DialogHeader>
        <div className="overflow-hidden rounded-lg border bg-zinc-950 dark:bg-black/50">
          <div className="flex items-center gap-1.5 border-b border-white/10 px-3 py-2">
            <i className="h-2.5 w-2.5 rounded-full bg-red-400/80" /><i className="h-2.5 w-2.5 rounded-full bg-amber-400/80" /><i className="h-2.5 w-2.5 rounded-full bg-emerald-400/80" />
            <span className="ml-2 font-mono text-[11px] text-zinc-500">tcp-pool@{rule.tag}</span>
            <span className="ml-auto flex gap-1">
              <CopyButton text={logs} className="text-zinc-400 hover:bg-white/10 hover:text-white" />
              <Button size="icon-sm" variant="ghost" className="h-7 w-7 text-zinc-400 hover:bg-white/10 hover:text-white" onClick={reload}><RefreshCw /><span className="sr-only">刷新</span></Button>
            </span>
          </div>
          <pre ref={preRef} className="h-[60vh] overflow-auto whitespace-pre-wrap break-all p-4 font-mono text-[11.5px] leading-relaxed text-zinc-300">
            {logs || <span className="text-zinc-500">加载中…</span>}
          </pre>
        </div>
      </DialogContent>
    </Dialog>
  );
}
