import "../ui/tokens.css";
import { useState } from "react";
import { SessionProvider, useSession } from "../session/SessionContext";
import { SessionBar } from "../session/SessionBar";
import { makeClient, type ApiClient } from "../api/client";
import { QueueScreen } from "../queue/QueueScreen";
import { ReviewScreen } from "../review/ReviewScreen";
function Shell({ client }: { client: ApiClient }) {
  const { session } = useSession();
  const [openId, setOpenId] = useState<string | null>(null);
  return (
    <div style={{ display: "grid", gridTemplateRows: "auto 1fr", height: "100vh" }}>
      <div><h1 style={{ margin: 8 }}>TourDesk Console</h1><SessionBar /></div>
      <main style={{ padding: 8, position: "relative" }}>
        {!session ? <p>Enter a tenant id and token to begin.</p>
          : openId ? <ReviewScreen client={client} conversationId={openId} agent="me" onDone={() => setOpenId(null)} />
          : <QueueScreen client={client} onOpen={setOpenId} />}
      </main>
    </div>
  );
}
function AppInner({ client }: { client?: ApiClient }) {
  const { session } = useSession();
  const resolved = client ?? makeClient(() => session);
  return <Shell client={resolved} />;
}
export function App({ client }: { client?: ApiClient }) {
  return <SessionProvider><AppInner client={client} /></SessionProvider>;
}
