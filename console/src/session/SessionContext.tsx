import {
  createContext,
  useContext,
  useMemo,
  useState,
  ReactNode,
} from "react";

export type Session = { tenantId: string; token: string };

type Ctx = {
  session: Session | null;
  setSession: (s: Session) => void;
  clear: () => void;
};

const SessionCtx = createContext<Ctx | null>(null);
const KEY = "td.session";

function load(): Session | null {
  try {
    return JSON.parse(sessionStorage.getItem(KEY) ?? "null");
  } catch {
    return null;
  }
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setS] = useState<Session | null>(load);
  const value = useMemo<Ctx>(
    () => ({
      session,
      setSession: (s) => {
        sessionStorage.setItem(KEY, JSON.stringify(s));
        setS(s);
      },
      clear: () => {
        sessionStorage.removeItem(KEY);
        setS(null);
      },
    }),
    [session]
  );
  return (
    <SessionCtx.Provider value={value}>{children}</SessionCtx.Provider>
  );
}

export function useSession(): Ctx {
  const c = useContext(SessionCtx);
  if (!c) throw new Error("useSession outside SessionProvider");
  return c;
}
