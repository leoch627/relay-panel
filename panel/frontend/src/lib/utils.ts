import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

const UNITS = ["B", "KB", "MB", "GB", "TB", "PB"];

// 保留约 3 位有效数字：1.23 GB / 12.3 GB / 123 GB
export function fmtBytes(b?: number | null): string {
  let n = Math.max(0, Number(b || 0));
  let i = 0;
  while (n >= 1024 && i < UNITS.length - 1) { n /= 1024; i++; }
  if (i === 0) return `${n} B`;
  return `${n < 10 ? n.toFixed(2) : n < 100 ? n.toFixed(1) : n.toFixed(0)} ${UNITS[i]}`;
}

export function fmtRate(kbps?: number | null): string {
  const n = Number(kbps || 0);
  if (n >= 1024 * 1024) return `${(n / 1048576).toFixed(1)} GB/s`;
  if (n >= 1024) return `${(n / 1024).toFixed(1)} MB/s`;
  return `${n} KB/s`;
}

export function fmtNumber(n?: number | null): string {
  return Number(n || 0).toLocaleString("zh-CN");
}

export function escTime(iso?: string): string {
  if (!iso) return "-";
  const d = new Date(iso);
  if (isNaN(+d)) return iso.replace("T", " ").slice(0, 19);
  const p = (x: number) => String(x).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

export function fmtRelative(iso?: string, now = Date.now()): string {
  if (!iso) return "从未";
  const t = +new Date(iso);
  if (isNaN(t)) return iso;
  const s = Math.round((now - t) / 1000);
  if (s < 5) return "刚刚";
  if (s < 60) return `${s} 秒前`;
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
  if (s < 86400 * 30) return `${Math.floor(s / 86400)} 天前`;
  return escTime(iso).slice(0, 10);
}

// 配额 GB 文本：最多 3 位小数，去掉多余的 0，避免编辑时把配额四舍五入掉
export function bytesToGBText(bytes: number): string {
  if (!bytes) return "";
  return String(Number((bytes / 1073741824).toFixed(3)));
}

export function initials(name: string): string {
  return (name.trim()[0] || "?").toUpperCase();
}
