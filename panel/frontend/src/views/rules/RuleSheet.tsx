import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { ChevronDown, Loader2, SlidersHorizontal } from "lucide-react";
import { api, ADV, errMsg, type NodeInfo, type Rule } from "@/api";
import { bytesToGBText, cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { NodeSelect } from "@/components/node-select";

export type SheetMode = "create" | "edit" | "duplicate";

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

function fromRule(r: Rule): RuleForm {
  return {
    tag: r.tag, remark: r.remark, listen_ip: r.listen_ip, listen_port: String(r.listen_port), remote_host: r.remote_host,
    remote_tcp_port: r.enable_tcp && r.remote_tcp_port ? String(r.remote_tcp_port) : "",
    remote_udp_port: r.enable_udp && r.remote_udp_port ? String(r.remote_udp_port) : "",
    enable_tcp: r.enable_tcp, enable_udp: r.enable_udp, node_id: String(r.node_id),
    rate_limit_kbps: r.rate_limit_kbps ? String(r.rate_limit_kbps) : "",
    traffic_quota_gb: bytesToGBText(r.traffic_quota_bytes),
    advanced: Object.fromEntries(Object.entries(r.advanced || {}).map(([k, v]) => [k, String(v)])),
  };
}

const TAG_RE = /^[A-Za-z0-9_-]{1,32}$/;
const isPort = (s: string) => /^\d+$/.test(s) && +s >= 1 && +s <= 65535;

function Field({ label, htmlFor, hint, error, children, className }: { label: string; htmlFor?: string; hint?: React.ReactNode; error?: string; children: React.ReactNode; className?: string }) {
  return (
    <div className={cn("space-y-2", className)}>
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
      {error ? <p className="text-xs text-destructive">{error}</p> : hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
    </div>
  );
}

function Section({ title, description, children }: { title: string; description?: string; children: React.ReactNode }) {
  return (
    <section className="space-y-4">
      <div>
        <h3 className="text-[13px] font-semibold">{title}</h3>
        {description && <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>}
      </div>
      {children}
    </section>
  );
}

function ProtoCard({ checked, onChange, title, desc }: { checked: boolean; onChange: (v: boolean) => void; title: string; desc: string }) {
  return (
    <label className={cn("flex cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors", checked ? "border-primary/50 bg-primary/[0.04] ring-1 ring-primary/20" : "hover:bg-muted/50")}>
      <Checkbox checked={checked} onCheckedChange={(v) => onChange(v === true)} className="mt-0.5" />
      <span>
        <span className="block text-sm font-medium">{title}</span>
        <span className="block text-xs text-muted-foreground">{desc}</span>
      </span>
    </label>
  );
}

export function RuleSheet({ open, mode, rule, nodes, onClose, onSaved }: {
  open: boolean; mode: SheetMode; rule: Rule | null; nodes: NodeInfo[]; onClose: () => void; onSaved: () => void;
}) {
  const [f, setF] = useState<RuleForm>(emptyForm);
  const [origQuota, setOrigQuota] = useState({ text: "", bytes: 0 });
  const [advOpen, setAdvOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const editing = mode === "edit";

  useEffect(() => {
    if (!open) return;
    setSubmitted(false);
    if (rule) {
      const form = fromRule(rule);
      if (mode === "duplicate") form.tag = "";
      setF(form);
      setOrigQuota({ text: form.traffic_quota_gb, bytes: rule.traffic_quota_bytes });
      setAdvOpen(Object.keys(rule.advanced || {}).length > 0);
    } else {
      setF({ ...emptyForm });
      setOrigQuota({ text: "", bytes: 0 });
      setAdvOpen(false);
    }
  }, [open, rule, mode]);

  const set = <K extends keyof RuleForm>(k: K, v: RuleForm[K]) => setF((p) => ({ ...p, [k]: v }));

  const errors = useMemo(() => {
    const e: Partial<Record<keyof RuleForm, string>> = {};
    if (!TAG_RE.test(f.tag.trim())) e.tag = "字母、数字、下划线或横杠，1-32 位";
    if (!isPort(f.listen_port)) e.listen_port = "端口需在 1-65535";
    if (!f.remote_host.trim()) e.remote_host = "请填写落地机 IP 或域名";
    if (!f.enable_tcp && !f.enable_udp) e.enable_tcp = "TCP / UDP 至少启用一个";
    if (f.enable_tcp && !isPort(f.remote_tcp_port)) e.remote_tcp_port = "端口需在 1-65535";
    if (f.enable_udp && (f.remote_udp_port || !f.enable_tcp) && !isPort(f.remote_udp_port)) e.remote_udp_port = "端口需在 1-65535";
    if (f.rate_limit_kbps && !/^\d+$/.test(f.rate_limit_kbps)) e.rate_limit_kbps = "请输入非负整数";
    if (f.traffic_quota_gb && !(Number(f.traffic_quota_gb) >= 0)) e.traffic_quota_gb = "请输入非负数";
    if (nodes.some((n) => String(n.id) === f.node_id && n.deleting)) e.node_id = "该节点正在清理，请选择其他节点";
    const badAdv = Object.entries(f.advanced).find(([, v]) => v !== "" && !/^\d+$/.test(v));
    if (badAdv) e.advanced = `${badAdv[0]} 需为非负整数`;
    return e;
  }, [f, nodes]);
  const err = (k: keyof RuleForm) => (submitted ? errors[k] : undefined);
  const advCount = Object.values(f.advanced).filter((v) => v !== "").length;

  const save = async (ev: React.FormEvent) => {
    ev.preventDefault();
    setSubmitted(true);
    const first = Object.values(errors)[0];
    if (first) { toast.error(first); return; }
    const quotaBytes = f.traffic_quota_gb === origQuota.text ? origQuota.bytes : Math.round((parseFloat(f.traffic_quota_gb) || 0) * 1073741824);
    const body = {
      tag: f.tag.trim(), remark: f.remark.trim(), listen_ip: f.listen_ip.trim(),
      listen_port: Number(f.listen_port), remote_host: f.remote_host.trim(),
      remote_tcp_port: f.enable_tcp ? Number(f.remote_tcp_port) : 0,
      remote_udp_port: f.enable_udp ? Number(f.remote_udp_port || f.remote_tcp_port) : 0,
      enable_tcp: f.enable_tcp, enable_udp: f.enable_udp,
      node_id: Number(f.node_id) || 0,
      rate_limit_kbps: Number(f.rate_limit_kbps) || 0,
      traffic_quota_bytes: quotaBytes,
      advanced: Object.fromEntries(Object.entries(f.advanced).filter(([, v]) => v !== "").map(([k, v]) => [k, parseInt(v, 10)])),
    };
    setSaving(true);
    try {
      await api(editing ? `/api/rules/${encodeURIComponent(body.tag)}` : "/api/rules", { method: editing ? "PUT" : "POST", body });
      toast.success(editing ? `已保存 ${body.tag}` : `已创建 ${body.tag}`, { description: body.node_id ? "节点将在约 5 秒内拉取生效" : undefined });
      onSaved(); onClose();
    } catch (e) { toast.error(errMsg(e)); } finally { setSaving(false); }
  };

  const rate = Number(f.rate_limit_kbps) || 0;

  return (
    <Sheet open={open} onOpenChange={(v) => !v && onClose()}>
      <SheetContent>
        <SheetHeader>
          <SheetTitle>{editing ? `编辑转发 · ${rule?.tag}` : mode === "duplicate" ? "复制为新转发" : "新建转发"}</SheetTitle>
          <SheetDescription>实例名为 tcp-pool@标签；远端节点的规则约 5 秒内经轮询生效</SheetDescription>
        </SheetHeader>
        <form id="rule-form" onSubmit={save} className="contents">
          <SheetBody className="space-y-8">
            <Section title="基本信息">
              <div className="grid gap-4 sm:grid-cols-2">
                <Field label="标签" htmlFor="r-tag" error={err("tag")} hint={editing ? "标签创建后不可修改" : "唯一标识，如 HK-JP-01"}>
                  <Input id="r-tag" value={f.tag} disabled={editing} placeholder="HK-JP-01" aria-invalid={!!err("tag")} onChange={(e) => set("tag", e.target.value)} autoFocus={!editing} />
                </Field>
                <Field label="备注" htmlFor="r-remark" hint="可选">
                  <Input id="r-remark" value={f.remark} placeholder="如 电信优化线路" onChange={(e) => set("remark", e.target.value)} />
                </Field>
                <Field label="运行节点" htmlFor="r-node" error={err("node_id")} className="sm:col-span-2">
                  <NodeSelect id="r-node" value={f.node_id} onChange={(v) => set("node_id", v)} nodes={nodes} />
                </Field>
              </div>
            </Section>

            <Section title="入口" description="用户连接的中转节点地址">
              <div className="grid gap-4 sm:grid-cols-[1fr_140px]">
                <Field label="监听 IP" htmlFor="r-lip" hint="所选节点上的网卡地址，0.0.0.0 / :: 表示全部">
                  <Input id="r-lip" className="font-mono" value={f.listen_ip} placeholder="0.0.0.0" onChange={(e) => set("listen_ip", e.target.value)} />
                </Field>
                <Field label="监听端口" htmlFor="r-lport" error={err("listen_port")}>
                  <Input id="r-lport" className="font-mono" inputMode="numeric" value={f.listen_port} placeholder="20000" aria-invalid={!!err("listen_port")} onChange={(e) => set("listen_port", e.target.value)} />
                </Field>
              </div>
            </Section>

            <Section title="落地" description="流量最终转发到的目标">
              <Field label="远端地址" htmlFor="r-host" error={err("remote_host")}>
                <Input id="r-host" className="font-mono" value={f.remote_host} placeholder="1.2.3.4 或 example.com" aria-invalid={!!err("remote_host")} onChange={(e) => set("remote_host", e.target.value)} />
              </Field>
              <div className="grid gap-3 sm:grid-cols-2">
                <ProtoCard checked={f.enable_tcp} onChange={(v) => set("enable_tcp", v)} title="TCP" desc="预连接池加速，支持 splice 零拷贝" />
                <ProtoCard checked={f.enable_udp} onChange={(v) => set("enable_udp", v)} title="UDP" desc="会话级转发，适合游戏 / DNS / QUIC" />
              </div>
              {err("enable_tcp") && <p className="-mt-2 text-xs text-destructive">{err("enable_tcp")}</p>}
              <div className="grid gap-4 sm:grid-cols-2">
                <Field label="远端 TCP 端口" htmlFor="r-tport" error={err("remote_tcp_port")}>
                  <Input id="r-tport" className="font-mono" inputMode="numeric" disabled={!f.enable_tcp} value={f.remote_tcp_port} placeholder={f.enable_tcp ? "443" : "未启用"} aria-invalid={!!err("remote_tcp_port")} onChange={(e) => set("remote_tcp_port", e.target.value)} />
                </Field>
                <Field label="远端 UDP 端口" htmlFor="r-uport" error={err("remote_udp_port")} hint={f.enable_udp && f.enable_tcp ? "留空则与 TCP 端口相同" : undefined}>
                  <Input id="r-uport" className="font-mono" inputMode="numeric" disabled={!f.enable_udp} value={f.remote_udp_port} placeholder={!f.enable_udp ? "未启用" : f.enable_tcp ? "同 TCP" : "53"} aria-invalid={!!err("remote_udp_port")} onChange={(e) => set("remote_udp_port", e.target.value)} />
                </Field>
              </div>
            </Section>

            <Section title="限制" description="0 或留空表示不限">
              <div className="grid gap-4 sm:grid-cols-2">
                <Field label="限速（KB/s，上下行各自）" htmlFor="r-rate" error={err("rate_limit_kbps")} hint={rate ? `≈ ${((rate * 8) / 1024).toFixed(rate < 128 ? 2 : 0)} Mbps` : "不限速"}>
                  <Input id="r-rate" inputMode="numeric" value={f.rate_limit_kbps} placeholder="10240" aria-invalid={!!err("rate_limit_kbps")} onChange={(e) => set("rate_limit_kbps", e.target.value)} />
                </Field>
                <Field label="流量配额（GB）" htmlFor="r-quota" error={err("traffic_quota_gb")} hint="达到后自动停机，重新启动会清零用量">
                  <Input id="r-quota" inputMode="decimal" value={f.traffic_quota_gb} placeholder="100" aria-invalid={!!err("traffic_quota_gb")} onChange={(e) => set("traffic_quota_gb", e.target.value)} />
                </Field>
              </div>
            </Section>

            <section className="rounded-lg border">
              <button type="button" onClick={() => setAdvOpen(!advOpen)} className="flex w-full items-center gap-3 px-4 py-3 text-left">
                <SlidersHorizontal className="h-4 w-4 text-muted-foreground" />
                <span className="flex-1">
                  <span className="block text-[13px] font-semibold">高级参数</span>
                  <span className="block text-xs text-muted-foreground">{advCount ? `已自定义 ${advCount} 项` : "默认使用程序内置值"}</span>
                </span>
                <ChevronDown className={cn("h-4 w-4 text-muted-foreground transition-transform", advOpen && "rotate-180")} />
              </button>
              {err("advanced") && <p className="px-4 pb-3 text-xs text-destructive">{err("advanced")}</p>}
              {advOpen && (
                <div className="divide-y border-t">
                  {ADV.map(([k, desc, def, range]) => (
                    <div key={k} className="flex items-center gap-3 px-4 py-2.5">
                      <div className="min-w-0 flex-1">
                        <div className="truncate font-mono text-xs font-medium">{k}</div>
                        <div className="truncate text-[11.5px] text-muted-foreground">{desc} · {range}</div>
                      </div>
                      <Input
                        className="h-8 w-28 font-mono text-xs"
                        inputMode="numeric"
                        placeholder={String(def)}
                        value={f.advanced[k] ?? ""}
                        onChange={(e) => set("advanced", { ...f.advanced, [k]: e.target.value })}
                      />
                    </div>
                  ))}
                </div>
              )}
            </section>
          </SheetBody>
          <SheetFooter>
            <Button type="button" variant="outline" onClick={onClose}>取消</Button>
            <Button type="submit" disabled={saving}>{saving && <Loader2 className="animate-spin" />}{editing ? "保存更改" : "创建并启动"}</Button>
          </SheetFooter>
        </form>
      </SheetContent>
    </Sheet>
  );
}
