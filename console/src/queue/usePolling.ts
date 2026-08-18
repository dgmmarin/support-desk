import { useEffect, useRef, useState } from "react";
export function usePolling<T>(fn: () => Promise<T>, ms: number): { data: T | null; error: unknown; refresh: () => void } {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<unknown>(null);
  const fnRef = useRef(fn); fnRef.current = fn;
  const [tick, setTick] = useState(0);
  useEffect(() => {
    let alive = true;
    const run = () => fnRef.current().then((d) => alive && setData(d)).catch((e) => alive && setError(e));
    run();
    const id = setInterval(run, ms);
    return () => { alive = false; clearInterval(id); };
  }, [ms, tick]);
  return { data, error, refresh: () => setTick((t) => t + 1) };
}
