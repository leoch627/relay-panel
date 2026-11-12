import { FormEvent, useState } from "react";
import { toast } from "sonner";
import { Eye, EyeOff, Loader2, Moon, Sun, Zap, ShieldCheck, Activity } from "lucide-react";
import { api, errMsg, type User } from "@/api";
import type { ThemeApi } from "@/lib/theme";
import { BrandMark } from "@/components/brand";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

// 登录页左侧的装饰拓扑：客户端 → 中转 → 落地，虚线流动
function RelayArt() {
  const paths = [
    "M40 200 C 140 200, 160 110, 260 110",
    "M40 200 C 140 200, 160 290, 260 290",
    "M300 110 C 400 110, 420 70, 520 70",
    "M300 110 C 400 110, 420 170, 520 170",
    "M300 290 C 400 290, 420 250, 520 250",
    "M300 290 C 400 290, 420 340, 520 340",
  ];
  const dots: [number, number][] = [[40, 200], [280, 110], [280, 290], [540, 70], [540, 170], [540, 250], [540, 340]];
  return (
    <svg viewBox="0 0 580 400" className="w-full max-w-lg" aria-hidden>
      {paths.map((d, i) => (
        <g key={i}>
          <path d={d} stroke="white" strokeOpacity={0.14} strokeWidth={2} fill="none" />
          <path d={d} stroke="white" strokeOpacity={0.75} strokeWidth={2} fill="none" strokeDasharray="4 16" className="animate-flow" style={{ animationDelay: `${i * 0.2}s` }} />
        </g>
      ))}
      {dots.map(([x, y], i) => (
        <g key={i}>
          <circle cx={x} cy={y} r={i === 0 ? 14 : i < 3 ? 18 : 10} fill="white" fillOpacity={0.1} />
          <circle cx={x} cy={y} r={i === 0 ? 6 : i < 3 ? 8 : 4.5} fill="white" />
        </g>
      ))}
    </svg>
  );
}

export function Login({ onLogin, theme }: { onLogin: (u: User, version: string) => void; theme: ThemeApi }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [show, setShow] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setLoading(true);
    setError("");
    try {
      const d = await api<{ user: User }>("/api/login", { body: { username, password } });
      const me = await api<{ user: User; version: string }>("/api/me");
      onLogin(d.user, me.version);
    } catch (err) {
      setError(errMsg(err));
      toast.error(errMsg(err));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="grid min-h-screen lg:grid-cols-[1.1fr_1fr]">
      <div className="relative hidden overflow-hidden bg-gradient-to-br from-indigo-600 via-indigo-700 to-violet-800 text-white lg:flex lg:flex-col">
        <div className="bg-grid absolute inset-0 opacity-[0.12] [--border:0_0%_100%]" />
        <div className="absolute -left-32 -top-32 h-96 w-96 rounded-full bg-violet-400/30 blur-3xl" />
        <div className="absolute -bottom-40 right-0 h-[28rem] w-[28rem] rounded-full bg-indigo-300/20 blur-3xl" />
        <div className="relative flex items-center gap-2.5 p-10">
          <BrandMark className="bg-white/15 from-white/20 to-white/5 shadow-none ring-1 ring-white/25" />
          <span className="text-[15px] font-semibold tracking-tight">Relay Panel</span>
        </div>
        <div className="relative flex flex-1 items-center justify-center px-10"><RelayArt /></div>
        <div className="relative space-y-6 p-10">
          <h2 className="max-w-md text-3xl font-semibold leading-tight tracking-tight">一处管理所有中转节点的 TCP / UDP 转发</h2>
          <div className="grid max-w-lg grid-cols-3 gap-4 text-[13px] text-white/75">
            <div className="flex items-start gap-2"><Zap className="mt-0.5 h-4 w-4 shrink-0 text-white" />预连接池，首包更快</div>
            <div className="flex items-start gap-2"><Activity className="mt-0.5 h-4 w-4 shrink-0 text-white" />流量统计与配额</div>
            <div className="flex items-start gap-2"><ShieldCheck className="mt-0.5 h-4 w-4 shrink-0 text-white" />节点出站轮询，免开端口</div>
          </div>
        </div>
      </div>

      <div className="relative flex flex-col">
        <div className="flex items-center justify-between p-6">
          <div className="flex items-center gap-2.5 lg:invisible">
            <BrandMark />
            <span className="text-[15px] font-semibold tracking-tight">Relay Panel</span>
          </div>
          <Button variant="ghost" size="icon-sm" onClick={() => theme.setTheme(theme.resolved === "dark" ? "light" : "dark")}>
            {theme.resolved === "dark" ? <Sun /> : <Moon />}<span className="sr-only">切换主题</span>
          </Button>
        </div>
        <div className="flex flex-1 items-center justify-center px-6 pb-16">
          <form onSubmit={submit} className="w-full max-w-sm space-y-6">
            <div className="space-y-2">
              <h1 className="text-2xl font-semibold tracking-tight">欢迎回来</h1>
              <p className="text-[13px] text-muted-foreground">登录以管理你的转发规则与节点</p>
            </div>
            <div className="space-y-4">
              <div className="space-y-2">
                <Label htmlFor="username">用户名</Label>
                <Input id="username" autoFocus autoComplete="username" className="h-10" value={username} onChange={(e) => setUsername(e.target.value)} required />
              </div>
              <div className="space-y-2">
                <Label htmlFor="password">密码</Label>
                <div className="relative">
                  <Input id="password" type={show ? "text" : "password"} autoComplete="current-password" className="h-10 pr-10" value={password} onChange={(e) => setPassword(e.target.value)} required aria-invalid={!!error} />
                  <button type="button" tabIndex={-1} onClick={() => setShow(!show)} className="absolute inset-y-0 right-0 grid w-10 place-items-center text-muted-foreground hover:text-foreground">
                    {show ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                    <span className="sr-only">{show ? "隐藏密码" : "显示密码"}</span>
                  </button>
                </div>
              </div>
              {error && <p className="text-[13px] text-destructive">{error}</p>}
            </div>
            <Button type="submit" size="lg" className="w-full" disabled={loading}>
              {loading && <Loader2 className="animate-spin" />}{loading ? "登录中…" : "登录"}
            </Button>
            <p className="text-center text-xs text-muted-foreground">首次安装的初始密码见服务端日志或 initial-admin-password 文件</p>
          </form>
        </div>
      </div>
    </div>
  );
}
