import { useEffect, useState } from "react";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogTitle } from "@/components/ui/alert-dialog";

// Promise 风格的确认框，替代浏览器原生 confirm()
interface ConfirmOptions { title: string; description?: string; confirmText?: string; destructive?: boolean }
type Pending = ConfirmOptions & { resolve: (ok: boolean) => void };

let push: ((p: Pending) => void) | null = null;

export function confirm(opts: ConfirmOptions): Promise<boolean> {
  return new Promise((resolve) => {
    if (!push) { resolve(window.confirm(opts.title)); return; }
    push({ ...opts, resolve });
  });
}

export function ConfirmHost() {
  const [cur, setCur] = useState<Pending | null>(null);
  useEffect(() => { push = setCur; return () => { push = null; }; }, []);
  const close = (ok: boolean) => { cur?.resolve(ok); setCur(null); };
  return (
    <AlertDialog open={!!cur} onOpenChange={(open) => { if (!open) close(false); }}>
      <AlertDialogContent>
        <div className="space-y-2">
          <AlertDialogTitle>{cur?.title}</AlertDialogTitle>
          {cur?.description && <AlertDialogDescription>{cur.description}</AlertDialogDescription>}
          {!cur?.description && <AlertDialogDescription className="sr-only">{cur?.title}</AlertDialogDescription>}
        </div>
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <AlertDialogCancel onClick={() => close(false)}>取消</AlertDialogCancel>
          <AlertDialogAction destructive={cur?.destructive} onClick={() => close(true)}>{cur?.confirmText || "确认"}</AlertDialogAction>
        </div>
      </AlertDialogContent>
    </AlertDialog>
  );
}
