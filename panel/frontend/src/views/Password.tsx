import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api, errMsg } from "@/api";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

export function PasswordDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (v: boolean) => void }) {
  const [oldPw, setOldPw] = useState("");
  const [newPw, setNewPw] = useState("");
  const [newPw2, setNewPw2] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => { if (open) { setOldPw(""); setNewPw(""); setNewPw2(""); } }, [open]);

  const mismatch = newPw2 !== "" && newPw !== newPw2;
  const tooShort = newPw !== "" && newPw.length < 6;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (newPw !== newPw2) { toast.error("两次输入的新密码不一致"); return; }
    setSaving(true);
    try {
      await api("/api/password", { body: { old: oldPw, new: newPw } });
      toast.success("密码已修改，请重新登录");
      setTimeout(() => location.reload(), 800);
    } catch (err) { toast.error(errMsg(err)); } finally { setSaving(false); }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>修改密码</DialogTitle>
          <DialogDescription>修改后所有设备上的会话都会失效，需要重新登录。</DialogDescription>
        </DialogHeader>
        <form id="pw-form" onSubmit={submit} className="space-y-4">
          <div className="space-y-2"><Label htmlFor="pw-old">当前密码</Label><Input id="pw-old" type="password" autoComplete="current-password" value={oldPw} onChange={(e) => setOldPw(e.target.value)} required /></div>
          <div className="space-y-2">
            <Label htmlFor="pw-new">新密码</Label>
            <Input id="pw-new" type="password" autoComplete="new-password" value={newPw} onChange={(e) => setNewPw(e.target.value)} aria-invalid={tooShort} required />
            <p className={tooShort ? "text-xs text-destructive" : "text-xs text-muted-foreground"}>至少 6 位</p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="pw-new2">确认新密码</Label>
            <Input id="pw-new2" type="password" autoComplete="new-password" value={newPw2} onChange={(e) => setNewPw2(e.target.value)} aria-invalid={mismatch} required />
            {mismatch && <p className="text-xs text-destructive">两次输入不一致</p>}
          </div>
        </form>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>取消</Button>
          <Button type="submit" form="pw-form" disabled={saving || mismatch || tooShort}>修改密码</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
