import { useState } from "react";
import { useSession } from "./SessionContext";

export function SessionBar() {
  const { session, setSession, clear } = useSession();
  const [tenantId, setTenant] = useState(session?.tenantId ?? "");
  const [token, setToken] = useState(session?.token ?? "");

  return (
    <form
      aria-label="dev session"
      onSubmit={(e) => {
        e.preventDefault();
        setSession({ tenantId, token });
      }}
      style={{
        display: "flex",
        gap: 8,
        alignItems: "center",
        padding: 8,
        borderBottom: "1px solid var(--line)",
      }}
    >
      <label>
        Tenant{" "}
        <input
          value={tenantId}
          onChange={(e) => setTenant(e.target.value)}
          required
        />
      </label>
      <label>
        Token{" "}
        <input
          value={token}
          onChange={(e) => setToken(e.target.value)}
          type="password"
          required
          style={{ width: 220 }}
        />
      </label>
      <button type="submit">Save</button>
      {session && (
        <button type="button" onClick={clear}>
          Clear
        </button>
      )}
      {session && (
        <span aria-live="polite" style={{ color: "var(--muted)" }}>
          tenant {session.tenantId}
        </span>
      )}
    </form>
  );
}
