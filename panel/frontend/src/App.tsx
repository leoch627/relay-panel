import { useEffect, useState } from "react";
import { Toaster } from "sonner";
import { api, setOnUnauthorized, type User } from "@/api";
import { useTheme } from "@/lib/theme";
import { Login } from "@/views/Login";
import { Layout } from "@/components/Layout";
import { ConfirmHost } from "@/components/confirm";
import { TooltipProvider } from "@/components/ui/tooltip";
import { LoadingState } from "@/components/common";

export default function App() {
  const [user, setUser] = useState<User | null | undefined>(undefined); // undefined = 加载中
  const [version, setVersion] = useState("");
  const theme = useTheme();

  useEffect(() => {
    setOnUnauthorized(() => setUser(null));
    api<{ user: User; version: string }>("/api/me")
      .then((d) => { setUser(d.user); setVersion(d.version); })
      .catch(() => setUser(null));
  }, []);

  return (
    <TooltipProvider delayDuration={200}>
      {user === undefined ? <LoadingState /> : !user
        ? <Login theme={theme} onLogin={(u, v) => { setUser(u); setVersion(v); }} />
        : <Layout user={user} version={version} theme={theme} onLogout={() => setUser(null)} />}
      <ConfirmHost />
      <Toaster
        theme={theme.resolved}
        position="top-center"
        toastOptions={{ classNames: { toast: "!rounded-lg !border-border !bg-popover !text-popover-foreground !shadow-lg", description: "!text-muted-foreground" } }}
      />
    </TooltipProvider>
  );
}
