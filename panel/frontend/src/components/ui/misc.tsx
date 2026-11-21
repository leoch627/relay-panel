import * as React from "react";
import { cn } from "@/lib/utils";

export function Skeleton({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("animate-pulse rounded-md bg-muted", className)} {...props} />;
}

// 用量条：超过 80% 变黄，满额变红
export function Meter({ value, className, tone }: { value: number; className?: string; tone?: "primary" | "auto" }) {
  const v = Math.min(100, Math.max(0, value));
  const color = tone === "primary" ? "bg-primary" : v >= 100 ? "bg-destructive" : v >= 80 ? "bg-warning" : "bg-primary";
  return (
    <div className={cn("h-1.5 w-full overflow-hidden rounded-full bg-muted", className)}>
      <div className={cn("h-full rounded-full transition-[width] duration-500", color)} style={{ width: `${v}%` }} />
    </div>
  );
}
