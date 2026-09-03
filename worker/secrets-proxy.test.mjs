import test from "node:test";
import assert from "node:assert/strict";
import { verifyAccessJwt, issuerFor, resetJwksCache } from "./secrets-proxy.js";

const TEAM = "example";
const ISSUER = "https://example.cloudflareaccess.com";
const KID = "test-key";

const keyPair = await crypto.subtle.generateKey(
  { name: "RSASSA-PKCS1-v1_5", modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: "SHA-256" },
  true,
  ["sign", "verify"],
);
const publicJwk = await crypto.subtle.exportKey("jwk", keyPair.publicKey);

function base64Url(bytes) {
  return Buffer.from(bytes).toString("base64url");
}

async function sign(payload, { kid = KID, alg = "RS256", key = keyPair.privateKey } = {}) {
  const header = base64Url(new TextEncoder().encode(JSON.stringify({ alg, kid })));
  const body = base64Url(new TextEncoder().encode(JSON.stringify(payload)));
  const signature = await crypto.subtle.sign(
    "RSASSA-PKCS1-v1_5",
    key,
    new TextEncoder().encode(`${header}.${body}`),
  );
  return `${header}.${body}.${base64Url(signature)}`;
}

function claims(overrides = {}) {
  const now = Math.floor(Date.now() / 1000);
  return { iss: ISSUER, aud: ["app-a"], exp: now + 600, iat: now, ...overrides };
}

let fetchCalls = 0;
function jwks(keys = [{ ...publicJwk, kid: KID, alg: "RS256" }]) {
  return async () => {
    fetchCalls += 1;
    return { ok: true, status: 200, json: async () => ({ keys }) };
  };
}

function options(overrides = {}) {
  return { expectedAud: "app-a", teamDomain: TEAM, fetchImpl: jwks(), ...overrides };
}

test.beforeEach(() => {
  resetJwksCache();
  fetchCalls = 0;
});

test("issuerFor accepts a bare team name, a full host and a URL", () => {
  for (const input of [TEAM, "example.cloudflareaccess.com", "https://example.cloudflareaccess.com/"]) {
    assert.equal(issuerFor(input), ISSUER);
  }
});

test("a token signed by the org, for this app, is accepted", async () => {
  const payload = await verifyAccessJwt(await sign(claims()), options());
  assert.deepEqual(payload.aud, ["app-a"]);
});

test("a valid token for another application is rejected", async () => {
  // The case that matters: this is what stops one host's service token from reading
  // another host's secrets, independently of how Access matches paths.
  const token = await sign(claims({ aud: ["app-b"] }));
  await assert.rejects(() => verifyAccessJwt(token, options()), /another Access application/);
});

test("a token from another Access organisation is rejected", async () => {
  const token = await sign(claims({ iss: "https://other.cloudflareaccess.com" }));
  await assert.rejects(() => verifyAccessJwt(token, options()), /another Access organisation/);
});

test("an expired token is rejected", async () => {
  const now = Math.floor(Date.now() / 1000);
  const token = await sign(claims({ exp: now - 3600 }));
  await assert.rejects(() => verifyAccessJwt(token, options()), /expired/);
});

test("a token with no expiry is rejected", async () => {
  const payload = claims();
  delete payload.exp;
  const token = await sign(payload);
  await assert.rejects(() => verifyAccessJwt(token, options()), /expired/);
});

test("an algorithm other than RS256 is rejected before any key is fetched", async () => {
  // Trusting the header's alg is how alg:none and HMAC-confusion attacks work.
  const token = await sign(claims(), { alg: "none" });
  await assert.rejects(() => verifyAccessJwt(token, options()), /unexpected signing algorithm/);
  assert.equal(fetchCalls, 0);
});

test("a payload swapped under a real signature is rejected", async () => {
  // Keep a genuine signature and change what it covers: the aud is rewritten to this
  // application, which is exactly what an attacker holding another host's token would
  // try.
  const [header, , signature] = (await sign(claims({ aud: ["app-b"] }))).split(".");
  const swapped = Buffer.from(JSON.stringify(claims({ aud: ["app-a"] }))).toString("base64url");
  await assert.rejects(
    () => verifyAccessJwt(`${header}.${swapped}.${signature}`, options()),
    /signature is invalid/,
  );
});

test("a token signed by a key the org does not publish is rejected", async () => {
  const other = await crypto.subtle.generateKey(
    { name: "RSASSA-PKCS1-v1_5", modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: "SHA-256" },
    true,
    ["sign", "verify"],
  );
  const token = await sign(claims(), { key: other.privateKey });
  await assert.rejects(() => verifyAccessJwt(token, options()), /signature is invalid/);
});

test("an unknown kid forces one refetch, then gives up", async () => {
  const token = await sign(claims(), { kid: "rotated" });
  await assert.rejects(() => verifyAccessJwt(token, options()), /unknown key/);
  assert.equal(fetchCalls, 2);
});

test("the key set is cached across verifications", async () => {
  await verifyAccessJwt(await sign(claims()), options());
  await verifyAccessJwt(await sign(claims()), options());
  assert.equal(fetchCalls, 1);
});

test("a malformed token is rejected", async () => {
  for (const token of ["", "one.two", "not-a-token"]) {
    await assert.rejects(() => verifyAccessJwt(token, options()), /malformed/);
  }
});

test("an unavailable key endpoint fails closed", async () => {
  const token = await sign(claims());
  const failing = options({ fetchImpl: async () => ({ ok: false, status: 503 }) });
  await assert.rejects(() => verifyAccessJwt(token, failing), /could not load Access keys/);
});
