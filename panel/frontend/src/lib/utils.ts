import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function fmtBytes(b?: number | null): string {
  const n = Number(b || 0);
  if (n >= 1 << 30) return (n / 1073741824).toFixed(1) + " GB";
  if (n >= 1 << 20) return (n / 1048576).toFixed(0) + " MB";
  if (n >= 1024) return (n / 1024).toFixed(0) + " KB";
  return n + " B";
}

export function escTime(iso?: string): string {
  if (!iso) return "-";
  return iso.replace("T", " ").slice(0, 19);
}
