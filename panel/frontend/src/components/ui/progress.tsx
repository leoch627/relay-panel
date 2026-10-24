import * as React from "react";
import { cn } from "@/lib/utils";

// 流量占比条（shadcn Progress 变体，支持告警色）
interface TrafficBarProps {
  pct: number;
  className?: string;
}

export function TrafficBar({ pct, className }: TrafficBarProps) {
  const v = Math.min(100, Math.max(0, pct));
  const color = v >= 100 ? "bg-red-500" : v >= 80 ? "bg-amber-500" : "bg-primary";
  return (
    <div className={cn("h-1.5 w-full overflow-hidden rounded-full bg-muted", className)}>
      <div className={cn("h-full rounded-full transition-all", color)} style={{ width: `${v}%` }} />
    </div>
  );
}
