// Serves Secrets Store secrets to trusted hosts.
//
// Each host's variables are stored as a single JSON blob, merged over a shared
// blob. So a new variable needs no redeploy of this Worker — only an edit of the
// blob in the dashboard.
//
// Authentication is done by Cloudflare Access in front of this Worker, so there
// is deliberately no auth logic here. Two checks remain as backstops:
//   - the hostname, because Access only guards the custom domain, so a request
//     arriving anywhere else (workers.dev, a preview URL) skipped the policy;
//   - the manifest lookup, which limits a host to its own blob even if the
//     per-path Access applications are ever misconfigured.

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
  } catch (error) {
    // Deliberately drops error.message: V8 embeds the offending input in JSON.parse
    // errors, so passing it on would put the first bytes of the secret in the
    // response body and in Workers logs.
    throw new Error(`${label} is not a JSON object of variables`);
  }
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    if (url.hostname !== env.EXPECTED_HOST) {
      return deny(403, "unexpected hostname");
    }

    // Fail closed if Access is not actually in front of us anymore.
    if (!request.headers.get("cf-access-jwt-assertion")) {
      return deny(403, "missing access assertion");
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

    const bindingName = manifest[match[1]];
    if (!bindingName) {
      // Deliberately does not list the known hosts: that is a host inventory, and a
      // caller that reached this point still may not be entitled to the whole map.
      return deny(404, "unknown host");
    }

    const binding = env[bindingName];
    if (!binding) {
      // Manifest and bindings come from the same map in Terraform, so this only
      // fires on a half-applied change.
      return deny(500, `binding missing: ${bindingName}`);
    }

    if (!env.SHARED_ENV) {
      return deny(500, "binding missing: SHARED_ENV");
    }

    let secrets;
    try {
      const shared = await readBlob(env.SHARED_ENV, "SHARED_ENV");
      const own = await readBlob(binding, bindingName);
      secrets = { ...shared, ...own };
    } catch (error) {
      return deny(500, error.message);
    }

    return new Response(JSON.stringify(secrets), { headers: JSON_HEADERS });
  },
};
