import { FormEvent, useState } from "react";
import { toast } from "sonner";
import { api, type User } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Satellite } from "lucide-react";

export function Login({ onLogin }: { onLogin: (u: User, version: string) => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setLoading(true);
    try {
      const d = await api<{ user: User }>("/api/login", { body: { username, password } });
      const me = await api<{ user: User; version: string }>("/api/me");
      onLogin(d.user, me.version);
    } catch (err: any) {
      toast.error(err.message);
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex h-full items-center justify-center p-5">
      <Card className="w-[380px] shadow-2xl">
        <CardHeader>
          <Satellite className="mb-2 h-8 w-8 text-primary" />
          <CardTitle className="text-xl">Relay Panel</CardTitle>
          <CardDescription>TCP/UDP 预连接转发管理面板</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit} className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="username">用户名</Label>
              <Input id="username" autoFocus value={username} onChange={(e) => setUsername(e.target.value)} required />
            </div>
            <div className="space-y-2">
              <Label htmlFor="password">密码</Label>
              <Input id="password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
            </div>
            <Button type="submit" className="w-full" disabled={loading}>
              {loading ? "登录中…" : "登录"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
