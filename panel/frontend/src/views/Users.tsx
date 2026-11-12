import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { Dices, Eye, EyeOff, KeyRound, Loader2, MoreHorizontal, Plus, ShieldCheck, ShieldOff, Trash2, UserPlus, Users as UsersIcon } from "lucide-react";
import { api, errMsg } from "@/api";
import { cn, escTime, initials } from "@/lib/utils";
import { usePolling } from "@/lib/usePolling";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Skeleton } from "@/components/ui/misc";
import { Tooltip } from "@/components/ui/tooltip";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { CopyButton, EmptyState, ErrorBanner, PageHeader } from "@/components/common";
import { confirm } from "@/components/confirm";

interface URow { id: number; username: string; is_admin: boolean; disabled: boolean; created_at: string; rule_count: number }

function randomPassword() {
  const chars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789";
  const buf = new Uint32Array(14);
  crypto.getRandomValues(buf);
  return Array.from(buf, (n) => chars[n % chars.length]).join("");
}

function PasswordInput({ id, value, onChange }: { id: string; value: string; onChange: (v: string) => void }) {
  const [show, setShow] = useState(false);
  return (
    <div className="flex gap-2">
      <div className="relative flex-1">
        <Input id={id} type={show ? "text" : "password"} autoComplete="new-password" className="pr-16 font-mono" value={value} placeholder="至少 6 位" onChange={(e) => onChange(e.target.value)} required minLength={6} />
        <div className="absolute inset-y-0 right-1 flex items-center">
          {value && <CopyButton text={value} />}
          <button type="button" onClick={() => setShow(!show)} className="grid h-7 w-7 place-items-center rounded text-muted-foreground hover:text-foreground">
            {show ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}<span className="sr-only">{show ? "隐藏" : "显示"}</span>
          </button>
        </div>
      </div>
      <Tooltip content="生成随机密码">
        <Button type="button" variant="outline" size="icon" onClick={() => { onChange(randomPassword()); setShow(true); }}><Dices /><span className="sr-only">生成随机密码</span></Button>
      </Tooltip>
    </div>
  );
}

export function UsersView({ meId }: { meId: number }) {
  const request = useCallback((signal: AbortSignal) => api<{ users: URow[] }>("/api/users", { signal }).then((d) => d.users || []), []);
  const { data: users, error, reload } = usePolling(request, 30000);
  const [addOpen, setAddOpen] = useState(false);
  const [nf, setNf] = useState({ username: "", password: "", is_admin: false });
  const [pwUser, setPwUser] = useState<URow | null>(null);
  const [newPw, setNewPw] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => { if (addOpen) setNf({ username: "", password: "", is_admin: false }); }, [addOpen]);
  useEffect(() => { if (pwUser) setNewPw(""); }, [pwUser]);

  const update = async (u: URow, body: Record<string, unknown>, ok: string) => {
    try { await api(`/api/users/${u.id}`, { method: "PUT", body }); toast.success(ok); reload(); }
    catch (e) { toast.error(errMsg(e)); }
  };

  const add = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      await api("/api/users", { body: { ...nf, username: nf.username.trim() } });
      toast.success(`已创建用户 ${nf.username.trim()}`); setAddOpen(false); reload();
    } catch (err) { toast.error(errMsg(err)); } finally { setSaving(false); }
  };

  const resetPw = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!pwUser) return;
    setSaving(true);
    try {
      await api(`/api/users/${pwUser.id}`, { method: "PUT", body: { password: newPw } });
      toast.success(`已重置 ${pwUser.username} 的密码`, { description: "该用户的所有会话已失效" }); setPwUser(null);
    } catch (err) { toast.error(errMsg(err)); } finally { setSaving(false); }
  };

  const toggleAdmin = async (u: URow) => {
    if (!(await confirm({
      title: u.is_admin ? `取消 ${u.username} 的管理员权限？` : `将 ${u.username} 设为管理员？`,
      description: u.is_admin ? "取消后该用户只能管理自己的转发。" : "管理员可以管理所有用户的转发、节点、用户与系统调优。",
      confirmText: u.is_admin ? "取消管理员" : "设为管理员", destructive: u.is_admin,
    }))) return;
    void update(u, { is_admin: !u.is_admin }, u.is_admin ? `${u.username} 已改为普通用户` : `${u.username} 已设为管理员`);
  };

  const toggleDisabled = async (u: URow) => {
    if (!u.disabled && !(await confirm({ title: `禁用 ${u.username}？`, description: "禁用后该用户无法登录，已有会话立即失效；其转发规则保持运行。", confirmText: "禁用", destructive: true }))) return;
    void update(u, { disabled: !u.disabled }, u.disabled ? `已启用 ${u.username}` : `已禁用 ${u.username}`);
  };

  const del = async (u: URow) => {
    if (!(await confirm({ title: `删除用户 ${u.username}？`, description: `其名下 ${u.rule_count} 条转发将移交给当前管理员。此操作不可撤销。`, confirmText: "删除用户", destructive: true }))) return;
    try { await api(`/api/users/${u.id}`, { method: "DELETE" }); toast.success(`已删除 ${u.username}`); reload(); }
    catch (e) { toast.error(errMsg(e)); }
  };

  return (
    <div>
      <PageHeader
        title="用户"
        description="多用户隔离：普通用户只能看到和操作自己的转发；管理员可以管理全部资源。"
        actions={<Button size="sm" onClick={() => setAddOpen(true)}><UserPlus />新建用户</Button>}
      />
      <ErrorBanner message={error} />

      <Card className="overflow-hidden">
        <Table>
          <TableHeader><TableRow className="hover:bg-transparent">
            <TableHead>用户</TableHead><TableHead>角色</TableHead><TableHead>转发数</TableHead><TableHead>允许登录</TableHead><TableHead>创建时间</TableHead><TableHead className="w-12" />
          </TableRow></TableHeader>
          <TableBody>
            {!users && [0, 1].map((i) => <TableRow key={i}><TableCell colSpan={6}><Skeleton className="h-9" /></TableCell></TableRow>)}
            {users?.length === 0 && <TableRow><TableCell colSpan={6}><EmptyState icon={UsersIcon} title="没有用户" /></TableCell></TableRow>}
            {users?.map((u) => {
              const me = u.id === meId;
              return (
                <TableRow key={u.id} className={cn(u.disabled && "opacity-60")}>
                  <TableCell>
                    <div className="flex items-center gap-3">
                      <div className={cn("grid h-8 w-8 shrink-0 place-items-center rounded-full text-[13px] font-semibold", u.is_admin ? "bg-primary/15 text-primary" : "bg-muted text-muted-foreground")}>{initials(u.username)}</div>
                      <div>
                        <div className="flex items-center gap-1.5 font-medium">{u.username}{me && <Badge variant="secondary" className="px-1 py-0 text-[10px]">你</Badge>}</div>
                        <div className="font-mono text-[11px] text-muted-foreground">ID {u.id}</div>
                      </div>
                    </div>
                  </TableCell>
                  <TableCell>{u.is_admin ? <Badge><ShieldCheck className="h-3 w-3" />管理员</Badge> : <Badge variant="secondary">普通用户</Badge>}</TableCell>
                  <TableCell className="tabular">{u.rule_count}</TableCell>
                  <TableCell>
                    <Tooltip content={me ? "不能禁用自己" : undefined}>
                      <span className="inline-flex"><Switch checked={!u.disabled} disabled={me} onCheckedChange={() => toggleDisabled(u)} aria-label="允许登录" /></span>
                    </Tooltip>
                  </TableCell>
                  <TableCell className="text-[13px] text-muted-foreground">{escTime(u.created_at)}</TableCell>
                  <TableCell>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button size="icon-sm" variant="ghost" className="text-muted-foreground"><MoreHorizontal /><span className="sr-only">更多</span></Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end" className="w-44">
                        <DropdownMenuItem onSelect={() => setPwUser(u)}><KeyRound />重置密码</DropdownMenuItem>
                        <DropdownMenuItem disabled={me} onSelect={() => toggleAdmin(u)}>{u.is_admin ? <><ShieldOff />取消管理员</> : <><ShieldCheck />设为管理员</>}</DropdownMenuItem>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem destructive disabled={me} onSelect={() => del(u)}><Trash2 />删除用户</DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </Card>

      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader><DialogTitle>新建用户</DialogTitle><DialogDescription>把初始密码通过安全渠道告知对方。</DialogDescription></DialogHeader>
          <form id="user-form" onSubmit={add} className="space-y-4">
            <div className="space-y-2"><Label htmlFor="u-name">用户名</Label><Input id="u-name" autoFocus autoComplete="off" value={nf.username} placeholder="2-32 个字符" onChange={(e) => setNf({ ...nf, username: e.target.value })} required /></div>
            <div className="space-y-2"><Label htmlFor="u-pw">初始密码</Label><PasswordInput id="u-pw" value={nf.password} onChange={(v) => setNf({ ...nf, password: v })} /></div>
            <label className={cn("flex cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors", nf.is_admin && "border-primary/40 bg-primary/[0.04]")}>
              <Switch checked={nf.is_admin} onCheckedChange={(v) => setNf({ ...nf, is_admin: v })} className="mt-0.5" />
              <span><span className="block text-sm font-medium">管理员</span><span className="block text-xs text-muted-foreground">可管理所有转发、节点、用户与系统调优</span></span>
            </label>
          </form>
          <DialogFooter>
            <Button variant="outline" onClick={() => setAddOpen(false)}>取消</Button>
            <Button type="submit" form="user-form" disabled={saving}>{saving ? <Loader2 className="animate-spin" /> : <Plus />}创建用户</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={!!pwUser} onOpenChange={(v) => !v && setPwUser(null)}>
        <DialogContent className="max-w-md">
          <DialogHeader><DialogTitle>重置密码 · {pwUser?.username}</DialogTitle><DialogDescription>重置后该用户的所有会话立即失效。</DialogDescription></DialogHeader>
          <form id="reset-form" onSubmit={resetPw} className="space-y-2">
            <Label htmlFor="u-newpw">新密码</Label>
            <PasswordInput id="u-newpw" value={newPw} onChange={setNewPw} />
          </form>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPwUser(null)}>取消</Button>
            <Button type="submit" form="reset-form" disabled={saving || newPw.length < 6}>{saving && <Loader2 className="animate-spin" />}重置密码</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
