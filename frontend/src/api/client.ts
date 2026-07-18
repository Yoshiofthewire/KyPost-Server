export function toErrorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

// readCsrfToken reads the non-HttpOnly csrf_token cookie the backend sets
// alongside the session cookie at login (double-submit CSRF pattern — see
// backend's csrfCheckOK). It carries no authority on its own; it only proves
// this request originated from JS that could read our own cookies, which a
// cross-site attacker's forged form/script cannot do. Exported for the rare
// caller (multipart/form-data uploads) that can't go through
// getJSON/postJSON/putJSON/deleteJSON and must attach the header itself.
export function readCsrfToken(): string {
  const match = document.cookie.match(/(?:^|; )csrf_token=([^;]*)/);
  return match ? decodeURIComponent(match[1]) : "";
}

async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const method = (init?.method ?? "GET").toUpperCase();
  const headers: Record<string, string> = { ...(init?.headers as Record<string, string> | undefined) };
  if (method !== "GET" && method !== "HEAD") {
    const csrfToken = readCsrfToken();
    if (csrfToken) {
      headers["X-CSRF-Token"] = csrfToken;
    }
  }
  const response = await fetch(path, {
    credentials: "include",
    ...init,
    headers
  });
  if (!response.ok) {
    let detail = "";
    try {
      const contentType = response.headers.get("content-type") || "";
      if (contentType.includes("application/json")) {
        const data = await response.json() as { error?: string; message?: string };
        detail = data.error || data.message || "";
      } else {
        detail = (await response.text()).trim();
      }
    } catch {
      detail = "";
    }
    throw new Error(detail ? `request failed: ${response.status} - ${detail}` : `request failed: ${response.status}`);
  }
  return response.json() as Promise<T>;
}

export async function getJSON<T>(path: string): Promise<T> {
  return requestJSON<T>(path);
}

export async function putJSON<T>(path: string, body: unknown): Promise<T> {
  return requestJSON<T>(path, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body)
  });
}

export async function postJSON<T>(path: string, body: unknown): Promise<T> {
  return requestJSON<T>(path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body)
  });
}

export async function deleteJSON<T>(path: string, body?: unknown): Promise<T> {
  return requestJSON<T>(path, {
    method: "DELETE",
    ...(body !== undefined ? { headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) } : {})
  });
}
