import { useState } from "react";
import { toast } from "sonner";
import { api } from "@/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

export function Password() {
  const [oldPw, setOldPw] = useState("");
  const [newPw, setNewPw] = useState("");
  const [newPw2, setNewPw2] = useState("");

  const submit = async () => {
    if (newPw !== newPw2) { toast.error("两次输入的新密码不一致"); return; }
    try {
      await api("/api/password", { body: { old: oldPw, new: newPw } });
      toast.success("密码已修改，即将重新登录");
      setTimeout(() => location.reload(), 800);
    } catch (e: any) { toast.error(e.message); }
  };

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-xl font-semibold tracking-tight">修改密码</h2>
        <p className="mt-1 text-xs text-muted-foreground">修改后需要重新登录</p>
      </div>
      <Card className="max-w-md">
        <CardHeader className="pb-3">
          <CardTitle className="text-sm">新密码</CardTitle>
          <CardDescription>至少 6 位</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="space-y-2"><Label>原密码</Label><Input type="password" value={oldPw} onChange={(e) => setOldPw(e.target.value)} /></div>
          <div className="space-y-2"><Label>新密码</Label><Input type="password" value={newPw} onChange={(e) => setNewPw(e.target.value)} /></div>
          <div className="space-y-2"><Label>确认新密码</Label><Input type="password" value={newPw2} onChange={(e) => setNewPw2(e.target.value)} /></div>
          <Button onClick={submit}>修改密码</Button>
        </CardContent>
      </Card>
    </div>
  );
}
