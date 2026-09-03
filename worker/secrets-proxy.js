// Serves Secrets Store secrets to trusted hosts.
//
// Each host's variables are stored as a single JSON blob, merged over a shared blob.
// So a new variable needs no redeploy of this Worker — only an edit of the blob in the
// dashboard.
//
// Cloudflare Access is the gate. This Worker verifies the JWT Access leaves behind
// anyway, and binds it to the host being requested: that is what makes per-host
// isolation cryptographic rather than a consequence of how Access matches paths, and
// what keeps a deleted or misconfigured Access application from silently exposing
// everything.

// ---------------------------------------------------------------------------------
// Access JWT verification.
//
// Access already refuses anything without a valid service token, so this looks
// redundant — until the Access application is deleted, renamed, or stops matching the
// path, at which point requests arrive unfiltered and this is the only thing between
// them and the secrets. A header-presence check does not survive that case, because
// the header is client-settable.
//
// It also makes per-host isolation cryptographic: a token issued for one application
// carries that application's `aud`, so it cannot be replayed against another host's
// path regardless of how Access matches paths.
//
// Kept in this file rather than imported: cloudflare_workers_script uploads a single
// module, so the Worker has to be self-contained. The helpers are exported for tests.
// ---------------------------------------------------------------------------------

const JWKS_TTL_MS = 60 * 60 * 1000;
const CLOCK_SKEW_S = 60;

// Cached per isolate. A cold isolate pays one fetch; a rotated key is picked up either
// by the TTL or immediately, because an unknown kid forces a refresh.
let jwksCache = { keys: null, fetchedAt: 0, issuer: null };

function base64UrlToBytes(value) {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(padded + "=".repeat((4 - (padded.length % 4)) % 4));
  return Uint8Array.from(binary, (character) => character.charCodeAt(0));
}

function decodeSegment(segment) {
  return JSON.parse(new TextDecoder().decode(base64UrlToBytes(segment)));
}

export function issuerFor(teamDomain) {
  // Accept either "team" or "team.cloudflareaccess.com", with or without a scheme.
  const host = teamDomain.replace(/^https?:\/\//, "").replace(/\/+$/, "");
  return `https://${host.includes(".") ? host : `${host}.cloudflareaccess.com`}`;
}

async function loadKeys(issuer, fetchImpl, force) {
  const fresh = jwksCache.keys &&
    jwksCache.issuer === issuer &&
    Date.now() - jwksCache.fetchedAt < JWKS_TTL_MS;
  if (fresh && !force) {
    return jwksCache.keys;
  }

  const response = await fetchImpl(`${issuer}/cdn-cgi/access/certs`);
  if (!response.ok) {
    throw new Error(`could not load Access keys: HTTP ${response.status}`);
  }
  const body = await response.json();
  if (!body || !Array.isArray(body.keys) || body.keys.length === 0) {
    throw new Error("Access key set is empty");
  }

  jwksCache = { keys: body.keys, fetchedAt: Date.now(), issuer };
  return body.keys;
}

// Exported for tests: the checks that do not need a network round trip.
export function checkClaims(payload, expectedAud, issuer, nowSeconds) {
  if (payload.iss !== issuer) {
    throw new Error("token was issued by another Access organisation");
  }

  const audiences = Array.isArray(payload.aud) ? payload.aud : [payload.aud];
  if (!audiences.includes(expectedAud)) {
    // The interesting failure: a token that is entirely valid, for a different
    // application. Without this a host's token would reach any host's path.
    throw new Error("token is for another Access application");
  }

  if (typeof payload.exp !== "number" || payload.exp + CLOCK_SKEW_S < nowSeconds) {
    throw new Error("token has expired");
  }
  if (typeof payload.nbf === "number" && payload.nbf - CLOCK_SKEW_S > nowSeconds) {
    throw new Error("token is not valid yet");
  }
}

export async function verifyAccessJwt(token, { expectedAud, teamDomain, fetchImpl = fetch }) {
  const parts = (token || "").split(".");
  if (parts.length !== 3) {
    throw new Error("token is malformed");
  }
  const [headerSegment, payloadSegment, signatureSegment] = parts;

  const header = decodeSegment(headerSegment);
  if (header.alg !== "RS256") {
    // Pinned rather than taken from the token: honouring alg from an attacker-supplied
    // header is how "alg: none" and HMAC-confusion attacks work.
    throw new Error(`unexpected signing algorithm ${header.alg}`);
  }

  const issuer = issuerFor(teamDomain);

  let keys = await loadKeys(issuer, fetchImpl, false);
  let key = keys.find((candidate) => candidate.kid === header.kid);
  if (!key) {
    // Keys rotate; an unknown kid is the signal to refetch rather than to reject.
    keys = await loadKeys(issuer, fetchImpl, true);
    key = keys.find((candidate) => candidate.kid === header.kid);
  }
  if (!key) {
    throw new Error("token was signed by an unknown key");
  }

  const publicKey = await crypto.subtle.importKey(
    "jwk",
    { kty: key.kty, n: key.n, e: key.e, alg: "RS256", ext: true },
    { name: "RSASSA-PKCS1-v1_5", hash: "SHA-256" },
    false,
    ["verify"],
  );

  const signed = new TextEncoder().encode(`${headerSegment}.${payloadSegment}`);
  const valid = await crypto.subtle.verify(
    "RSASSA-PKCS1-v1_5",
    publicKey,
    base64UrlToBytes(signatureSegment),
    signed,
  );
  if (!valid) {
    throw new Error("token signature is invalid");
  }

  const payload = decodeSegment(payloadSegment);
  checkClaims(payload, expectedAud, issuer, Math.floor(Date.now() / 1000));
  return payload;
}

// Test seam: an isolate is long-lived, so a test must be able to drop the cache.
export function resetJwksCache() {
  jwksCache = { keys: null, fetchedAt: 0, issuer: null };
}

const JSON_HEADERS = {
  "content-type": "application/json; charset=utf-8",
  "cache-control": "no-store",
};

function deny(status, message) {
  return new Response(JSON.stringify({ error: message }), { status, headers: JSON_HEADERS });
}

// A `json` binding may arrive already parsed or as the raw JSON text, depending on
// how the value was uploaded. Accept both rather than depending on that.
function readManifest(env) {
  const raw = env.MANIFEST;
  const parsed = typeof raw === "string" ? JSON.parse(raw) : raw;
  if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error("MANIFEST is not an object");
  }
  return parsed;
}

async function readBlob(binding, label) {
  const raw = await binding.get();
  try {
    const parsed = JSON.parse(raw);
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
      throw new Error("not a JSON object");
    }
    return parsed;
  } catch {
    // Deliberately drops the parser message: V8 embeds the offending input in
    // JSON.parse errors, so passing it on would put the first bytes of the secret in
    // the response body and in the logs.
    throw new Error(`${label} is not a JSON object of variables`);
  }
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    if (url.hostname !== env.EXPECTED_HOST) {
      return deny(403, "unexpected hostname");
    }

    if (request.method !== "GET") {
      return deny(405, "method not allowed");
    }

    const match = url.pathname.match(/^\/v1\/env\/([a-z0-9-]+)\/?$/);
    if (!match) {
      return deny(404, "not found");
    }

    let manifest;
    try {
      manifest = readManifest(env);
    } catch (error) {
      return deny(500, error.message);
    }

    const entry = manifest[match[1]];
    if (!entry || !entry.secret || !entry.aud) {
      // Deliberately does not list the known hosts: that is a host inventory, and a
      // caller that reached this point still may not be entitled to the whole map.
      return deny(404, "unknown host");
    }

    if (!env.TEAM_DOMAIN) {
      // Refuse rather than fall back to trusting Access blindly: an unset team domain
      // means this Worker cannot verify anything.
      return deny(500, "binding missing: TEAM_DOMAIN");
    }

    try {
      await verifyAccessJwt(request.headers.get("cf-access-jwt-assertion"), {
        expectedAud: entry.aud,
        teamDomain: env.TEAM_DOMAIN,
      });
    } catch (error) {
      return deny(403, error.message);
    }

    const binding = env[entry.secret];
    if (!binding) {
      // Manifest and bindings come from the same map in Terraform, so this only fires
      // on a half-applied change.
      return deny(500, `binding missing: ${entry.secret}`);
    }
    if (!env.SHARED_ENV) {
      return deny(500, "binding missing: SHARED_ENV");
    }

    let secrets;
    try {
      const shared = await readBlob(env.SHARED_ENV, "SHARED_ENV");
      const own = await readBlob(binding, entry.secret);
      secrets = { ...shared, ...own };
    } catch (error) {
      return deny(500, error.message);
    }

    return new Response(JSON.stringify(secrets), { headers: JSON_HEADERS });
  },
};
