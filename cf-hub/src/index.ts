interface Env {
  KEEPER_STORE: KVNamespace;
}

interface ShareRecord {
  ownerHash: string;
  shareId: string;
  encryptedPayload: string;
  operator: string;
  memo: string;
  createdAt: number;
  expiresAt: number;
}

interface UserProfileRecord {
  ownerHash: string;
  username: string;
  encryptedProfile: string;
  version: number;
  updatedAt: number;
}

function corsHeaders(): HeadersInit {
  return {
    "Access-Control-Allow-Origin": "*",
    "Access-Control-Allow-Methods": "GET, POST, OPTIONS, DELETE",
    "Access-Control-Allow-Headers": "Content-Type, Authorization",
  };
}

function jsonResponse(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: {
      "Content-Type": "application/json; charset=utf-8",
      ...corsHeaders(),
    },
  });
}

async function hashToken(token: string): Promise<string> {
  const enc = new TextEncoder();
  const digest = await crypto.subtle.digest("SHA-256", enc.encode(token));
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    if (request.method === "OPTIONS") {
      return new Response(null, { headers: corsHeaders() });
    }

    const url = new URL(request.url);
    const path = url.pathname;

    // 1. Health check
    if (path === "/" || path === "/health") {
      return jsonResponse({
        status: "ok",
        service: "cumt-keeper-hub",
        now: Date.now(),
      });
    }

    // 2. Fetch a friend's share (Public read-only: no auth required)
    // GET /api/share/:share_id
    if (request.method === "GET" && path.startsWith("/api/share/") && path !== "/api/share/my") {
      const shareId = path.replace("/api/share/", "").trim();
      if (!shareId || shareId.includes("/")) {
        return jsonResponse({ error: "invalid_share_id" }, 400);
      }

      const raw = await env.KEEPER_STORE.get(`share:${shareId}`);
      if (!raw) {
        return jsonResponse(
          { error: "share_not_found_or_revoked", message: "该分享码不存在或已被所有者吊销" },
          404
        );
      }

      try {
        const record: ShareRecord = JSON.parse(raw);
        if (record.expiresAt && record.expiresAt < Date.now()) {
          return jsonResponse(
            { error: "share_expired", message: "该分享码已过期" },
            410
          );
        }

        return jsonResponse({
          success: true,
          shareId: record.shareId,
          encryptedPayload: record.encryptedPayload,
          operator: record.operator,
          memo: record.memo,
          expiresAt: record.expiresAt,
        });
      } catch {
        return jsonResponse({ error: "corrupted_share_record" }, 500);
      }
    }

    // Auth verification helper for admin operations
    const authHeader = request.headers.get("Authorization") || "";
    const tokenMatch = authHeader.match(/^Bearer\s+(.+)$/i);
    const adminToken = tokenMatch ? tokenMatch[1].trim() : "";

    // 3. Publish a new share (Owner only)
    // POST /api/share/publish
    if (request.method === "POST" && path === "/api/share/publish") {
      if (!adminToken) {
        return jsonResponse({ error: "unauthorized", message: "缺少管理员令牌" }, 401);
      }

      let body: any;
      try {
        body = await request.json();
      } catch {
        return jsonResponse({ error: "invalid_json" }, 400);
      }

      const { share_id, encrypted_payload, operator, memo, duration_hours } = body;
      if (!share_id || !encrypted_payload) {
        return jsonResponse({ error: "missing_fields" }, 400);
      }

      const ownerHash = await hashToken(adminToken);
      const hours = typeof duration_hours === "number" && duration_hours > 0 ? duration_hours : 168; // default 7 days
      const ttlSeconds = Math.min(hours * 3600, 365 * 86400);
      const expiresAt = Date.now() + ttlSeconds * 1000;

      const record: ShareRecord = {
        ownerHash,
        shareId: share_id,
        encryptedPayload: encrypted_payload,
        operator: operator || "cmcc",
        memo: memo || "好友分享",
        createdAt: Date.now(),
        expiresAt,
      };

      await env.KEEPER_STORE.put(`share:${share_id}`, JSON.stringify(record), {
        expirationTtl: ttlSeconds,
      });

      // Update owner's index of shares
      const indexKey = `owner_shares:${ownerHash}`;
      const indexRaw = await env.KEEPER_STORE.get(indexKey);
      let shareList: string[] = [];
      if (indexRaw) {
        try {
          shareList = JSON.parse(indexRaw);
        } catch {}
      }
      if (!shareList.includes(share_id)) {
        shareList.push(share_id);
      }
      await env.KEEPER_STORE.put(indexKey, JSON.stringify(shareList));

      return jsonResponse({
        success: true,
        shareId: share_id,
        expiresAt,
        memo: record.memo,
      });
    }

    // 4. Revoke a share (Owner only)
    // POST /api/share/revoke
    if (request.method === "POST" && path === "/api/share/revoke") {
      if (!adminToken) {
        return jsonResponse({ error: "unauthorized" }, 401);
      }

      let body: any;
      try {
        body = await request.json();
      } catch {
        return jsonResponse({ error: "invalid_json" }, 400);
      }

      const { share_id } = body;
      if (!share_id) {
        return jsonResponse({ error: "missing_share_id" }, 400);
      }

      const raw = await env.KEEPER_STORE.get(`share:${share_id}`);
      if (!raw) {
        return jsonResponse({ success: true, message: "already_deleted" });
      }

      const ownerHash = await hashToken(adminToken);
      const record: ShareRecord = JSON.parse(raw);
      if (record.ownerHash !== ownerHash) {
        return jsonResponse({ error: "forbidden", message: "无权吊销他人的分享" }, 403);
      }

      await env.KEEPER_STORE.delete(`share:${share_id}`);

      // Remove from owner's index
      const indexKey = `owner_shares:${ownerHash}`;
      const indexRaw = await env.KEEPER_STORE.get(indexKey);
      if (indexRaw) {
        try {
          const list: string[] = JSON.parse(indexRaw);
          const filtered = list.filter((id) => id !== share_id);
          await env.KEEPER_STORE.put(indexKey, JSON.stringify(filtered));
        } catch {}
      }

      return jsonResponse({ success: true, revoked: share_id });
    }

    // 5. List owner's shares (Owner only)
    // GET /api/share/my
    if (request.method === "GET" && path === "/api/share/my") {
      if (!adminToken) {
        return jsonResponse({ error: "unauthorized" }, 401);
      }

      const ownerHash = await hashToken(adminToken);
      const indexKey = `owner_shares:${ownerHash}`;
      const indexRaw = await env.KEEPER_STORE.get(indexKey);
      const shareIds: string[] = indexRaw ? JSON.parse(indexRaw) : [];

      const results = [];
      for (const id of shareIds) {
        const raw = await env.KEEPER_STORE.get(`share:${id}`);
        if (raw) {
          try {
            const r: ShareRecord = JSON.parse(raw);
            results.push({
              shareId: r.shareId,
              memo: r.memo,
              operator: r.operator,
              createdAt: r.createdAt,
              expiresAt: r.expiresAt,
            });
          } catch {}
        }
      }

      return jsonResponse({ success: true, shares: results });
    }

    // 6. User encrypted profile sync (backup / restore)
    // POST /api/user/sync (backup)
    if (request.method === "POST" && path === "/api/user/sync") {
      if (!adminToken) {
        return jsonResponse({ error: "unauthorized" }, 401);
      }

      let body: any;
      try {
        body = await request.json();
      } catch {
        return jsonResponse({ error: "invalid_json" }, 400);
      }

      const { username, encrypted_profile, version } = body;
      if (!username || !encrypted_profile) {
        return jsonResponse({ error: "missing_fields" }, 400);
      }

      const ownerHash = await hashToken(adminToken);
      const userRecord: UserProfileRecord = {
        ownerHash,
        username,
        encryptedProfile: encrypted_profile,
        version: version || 1,
        updatedAt: Date.now(),
      };

      await env.KEEPER_STORE.put(`user:${ownerHash}`, JSON.stringify(userRecord));
      return jsonResponse({ success: true, updatedAt: userRecord.updatedAt });
    }

    // GET /api/user/sync (restore)
    if (request.method === "GET" && path === "/api/user/sync") {
      if (!adminToken) {
        return jsonResponse({ error: "unauthorized" }, 401);
      }

      const ownerHash = await hashToken(adminToken);
      const raw = await env.KEEPER_STORE.get(`user:${ownerHash}`);
      if (!raw) {
        return jsonResponse({ error: "not_found", message: "无云端备份" }, 404);
      }

      const record: UserProfileRecord = JSON.parse(raw);
      return jsonResponse({
        success: true,
        username: record.username,
        encryptedProfile: record.encryptedProfile,
        version: record.version,
        updatedAt: record.updatedAt,
      });
    }

    return jsonResponse({ error: "not_found" }, 404);
  },
};
