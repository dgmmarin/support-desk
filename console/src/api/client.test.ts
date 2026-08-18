import { describe, expect, test } from "vitest";
import { makeClient, ClaimConflict, MissingTenant } from "./client";

const session = { tenantId: "t1", token: "jwt" };

function stubFetch(status: number, body: unknown) {
  return async () =>
    new Response(JSON.stringify(body), {
      status,
      headers: { "content-type": "application/json" },
    });
}

describe("api client", () => {
  test("getQueue returns items and sends auth headers", async () => {
    let seenHeaders: Record<string, string> | undefined;
    const fetchImpl = async (_input: string | URL | Request, init?: RequestInit) => {
      // jsdom/undici's Request ctor rejects relative URLs with no base, so read
      // the headers straight off `init` instead of round-tripping through `new
      // Request(input, init)` (client.ts sends them as a plain header object).
      seenHeaders = init?.headers as Record<string, string>;
      // Wire shape (backend/internal/queue/queue.go QueueItem): conversation_id, not conversationId.
      return new Response(JSON.stringify({ items: [{ conversation_id: "c1" }] }), {
        status: 200,
      });
    };
    const c = makeClient(() => session, fetchImpl);
    const items = await c.getQueue();
    expect(items[0].conversation_id).toBe("c1");
    expect(seenHeaders?.["X-Tenant-ID"]).toBe("t1");
    expect(seenHeaders?.["Authorization"]).toBe("Bearer jwt");
  });

  test("claim maps 409 to ClaimConflict", async () => {
    const c = makeClient(() => session, stubFetch(409, { error: "claimed" }));
    await expect(c.claim("c1")).rejects.toBeInstanceOf(ClaimConflict);
  });

  test("missing session throws MissingTenant before fetch", async () => {
    const c = makeClient(() => null, stubFetch(200, {}));
    await expect(c.getQueue()).rejects.toBeInstanceOf(MissingTenant);
  });
});
