import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { api } from "@/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

interface URow {
  id: number; username: string; is_admin: boolean; disabled: boolean;
  created_at: string; rule_count: number;
}

export function UsersView({ meId }: { meId: number }) {
  const [users, setUsers] = useState<URow[] | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  const [nf, setNf] = useState({ username: "", password: "", is_admin: false });
  const [pwUser, setPwUser] = useState<URow | null>(null);
  const [newPw, setNewPw] = useState("");

  const load = useCallback(() => {
    api<{ users: URow[] }>("/api/users").then((d) => setUsers(d.users)).catch(() => setUsers([]));
  }, []);
  useEffect(load, [load]);

  const add = async () => {
    try {
      await api("/api/users", { body: nf });
      toast.success("用户已创建"); setAddOpen(false); setNf({ username: "", password: "", is_admin: false }); load();
    } catch (e: any) { toast.error(e.message); }
  };

  const toggle = async (u: URow) => {
    try {
      await api(`/api/users/${u.id}`, { method: "PUT", body: { disabled: !u.disabled } });
      toast.success(u.disabled ? `已启用 ${u.username}` : `已禁用 ${u.username}`); load();
    } catch (e: any) { toast.error(e.message); }
  };

  const del = async (u: URow) => {
    if (!confirm(`确认删除用户 [${u.username}]？其名下 ${u.rule_count} 条转发将移交给当前管理员。`)) return;
    try { await api(`/api/users/${u.id}`, { method: "DELETE" }); toast.success(`已删除 ${u.username}`); load(); }
    catch (e: any) { toast.error(e.message); }
  };

  const resetPw = async () => {
    if (!pwUser) return;
    try {
      await api(`/api/users/${pwUser.id}`, { method: "PUT", body: { password: newPw } });
      toast.success("密码已重置"); setPwUser(null); setNewPw("");
    } catch (e: any) { toast.error(e.message); }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-xl font-semibold tracking-tight">用户管理</h2>
          <p className="mt-1 text-xs text-muted-foreground">多用户隔离：普通用户只能看到和操作自己的转发</p>
        </div>
        <Button size="sm" onClick={() => setAddOpen(true)}>新建用户</Button>
      </div>

      <Card className="overflow-hidden pb-0">
        <Table>
          <TableHeader><TableRow>
            <TableHead>ID</TableHead><TableHead>用户名</TableHead><TableHead>角色</TableHead>
            <TableHead>转发数</TableHead><TableHead>状态</TableHead><TableHead>创建时间</TableHead><TableHead>操作</TableHead>
          </TableRow></TableHeader>
          <TableBody>
            {users === null && <TableRow><TableCell colSpan={7}><div className="py-10 text-center text-sm text-muted-foreground">加载中…</div></TableCell></TableRow>}
            {users?.map((u) => (
              <TableRow key={u.id}>
                <TableCell className="font-mono text-xs">{u.id}</TableCell>
                <TableCell className="font-medium">{u.username}</TableCell>
                <TableCell>{u.is_admin ? <Badge variant="info">管理员</Badge> : <Badge variant="secondary">普通用户</Badge>}</TableCell>
                <TableCell>{u.rule_count}</TableCell>
                <TableCell>{u.disabled ? <Badge variant="destructive">已禁用</Badge> : <Badge variant="success">正常</Badge>}</TableCell>
                <TableCell className="text-xs text-muted-foreground">{u.created_at.replace("T", " ").slice(0, 19)}</TableCell>
                <TableCell>
                  <div className="flex flex-wrap gap-1.5">
                    <Button size="sm" variant="secondary" onClick={() => setPwUser(u)}>改密码</Button>
                    <Button size="sm" variant="secondary" onClick={() => toggle(u)}>{u.disabled ? "启用" : "禁用"}</Button>
                    <Button size="sm" variant="destructive" disabled={u.id === meId} onClick={() => del(u)}>删除</Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Card>

      <Dialog open={addOpen} onOpenChange={setAddOpen}>
        <DialogContent className="max-w-sm">
          <DialogHeader><DialogTitle>新建用户</DialogTitle></DialogHeader>
          <div className="space-y-3">
            <div className="space-y-2"><Label>用户名</Label><Input value={nf.username} placeholder="2-32 字符" onChange={(e) => setNf({ ...nf, username: e.target.value })} /></div>
            <div className="space-y-2"><Label>密码</Label><Input value={nf.password} placeholder="至少 6 位" onChange={(e) => setNf({ ...nf, password: e.target.value })} /></div>
            <label className="flex items-center gap-2 text-sm">
              <Switch checked={nf.is_admin} onCheckedChange={(v) => setNf({ ...nf, is_admin: v })} />
              设为管理员（可管理所有转发/用户/调优）
            </label>
          </div>
          <DialogFooter>
            <Button variant="secondary" onClick={() => setAddOpen(false)}>取消</Button>
            <Button onClick={add}>创建</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={!!pwUser} onOpenChange={() => setPwUser(null)}>
        <DialogContent className="max-w-sm">
          <DialogHeader><DialogTitle>重置密码 · {pwUser?.username}</DialogTitle></DialogHeader>
          <div className="space-y-2"><Label>新密码</Label><Input value={newPw} placeholder="至少 6 位" onChange={(e) => setNewPw(e.target.value)} /></div>
          <DialogFooter>
            <Button variant="secondary" onClick={() => setPwUser(null)}>取消</Button>
            <Button onClick={resetPw}>重置</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
