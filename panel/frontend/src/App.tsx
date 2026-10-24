import { useEffect, useState } from "react";
import { api, setOnUnauthorized, type User } from "@/api";
import { Login } from "@/views/Login";
import { Layout } from "@/components/Layout";

export default function App() {
  const [user, setUser] = useState<User | null | undefined>(undefined); // undefined = 加载中
  const [version, setVersion] = useState("");

  useEffect(() => {
    setOnUnauthorized(() => setUser(null));
    api<{ user: User; version: string }>("/api/me")
      .then((d) => { setUser(d.user); setVersion(d.version); })
      .catch(() => setUser(null));
  }, []);

  if (user === undefined) return <div className="flex h-full items-center justify-center text-sm text-muted-foreground">加载中…</div>;
  if (!user) return <Login onLogin={(u, v) => { setUser(u); setVersion(v); }} />;
  return <Layout user={user} version={version} onLogout={() => setUser(null)} />;
}
