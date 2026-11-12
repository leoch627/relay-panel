import { useCallback, useEffect, useState } from "react";

export type ThemePref = "light" | "dark" | "system";
const KEY = "relay-theme"; // 与 index.html 首帧脚本一致

function readPref(): ThemePref {
  try {
    const v = localStorage.getItem(KEY);
    if (v === "light" || v === "dark" || v === "system") return v;
  } catch { /* 隐私模式等场景下存储不可用 */ }
  return "system";
}

const media = () => window.matchMedia("(prefers-color-scheme: dark)");

export function useTheme() {
  const [pref, setPref] = useState<ThemePref>(readPref);
  const [systemDark, setSystemDark] = useState(() => media().matches);

  useEffect(() => {
    const m = media();
    const onChange = () => setSystemDark(m.matches);
    m.addEventListener("change", onChange);
    return () => m.removeEventListener("change", onChange);
  }, []);

  const resolved: "light" | "dark" = pref === "system" ? (systemDark ? "dark" : "light") : pref;
  useEffect(() => { document.documentElement.classList.toggle("dark", resolved === "dark"); }, [resolved]);

  const setTheme = useCallback((next: ThemePref) => {
    setPref(next);
    try { localStorage.setItem(KEY, next); } catch { /* 忽略 */ }
  }, []);

  return { pref, resolved, setTheme };
}
export type ThemeApi = ReturnType<typeof useTheme>;
