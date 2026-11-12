import { useCallback, useEffect, useState } from "react";

// 极简 hash 路由：刷新保持当前页，浏览器前进/后退可用
export function useHashRoute<T extends string>(valid: readonly T[], fallback: T) {
  const parse = useCallback((): T => {
    const v = location.hash.replace(/^#\/?/, "").split("?")[0] as T;
    return valid.includes(v) ? v : fallback;
  }, [valid, fallback]);
  const [route, setRoute] = useState<T>(parse);

  useEffect(() => {
    const onHash = () => setRoute(parse());
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, [parse]);

  const navigate = useCallback((next: T) => {
    if (location.hash !== `#/${next}`) location.hash = `/${next}`;
    setRoute(next);
  }, []);

  return [route, navigate] as const;
}
