import { useCallback, useEffect, useRef, useState } from "react";

// Schedule the next request only when the previous one has settled. A manual
// refresh during a request queues one follow-up, so mutations cannot miss a refresh.
export function usePolling<T>(request: (signal: AbortSignal) => Promise<T>, interval: number | null = 5000) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const refresh = useRef<() => void>(() => {});
  const reload = useCallback(() => refresh.current(), []);

  useEffect(() => {
    let stopped = false;
    let running = false;
    let pending = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let controller: AbortController | undefined;
    setData(null);
    setError(null);

    const run = async () => {
      if (stopped) return;
      if (running) { pending = true; return; }
      clearTimeout(timer);
      running = true;
      pending = false;
      const current = new AbortController();
      controller = current;
      const timeout = setTimeout(() => current.abort(), 15000);
      try {
        const next = await request(current.signal);
        if (!stopped) { setData(next); setError(null); }
      } catch (e) {
        const timedOut = current.signal.aborted;
        // A request may contain several fetches. Cancel any sibling still in
        // flight when one fails before scheduling the next attempt.
        current.abort();
        if (!stopped) setError(timedOut ? "请求超时，正在重试" : e instanceof Error ? e.message : "加载失败");
      } finally {
        clearTimeout(timeout);
        running = false;
        if (!stopped && (pending || interval !== null)) timer = setTimeout(run, pending ? 0 : interval!);
      }
    };
    refresh.current = () => { void run(); };
    void run();
    return () => {
      stopped = true;
      clearTimeout(timer);
      controller?.abort();
      refresh.current = () => {};
    };
  }, [request, interval]);

  return { data, error, reload };
}
