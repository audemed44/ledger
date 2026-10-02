// The server API. A 401 tells the app to show the sign-in screen again.
export async function api<T>(path: string, body?: unknown, method?: "DELETE"): Promise<T> {
  const res = await fetch("/api/" + path, {
    method: method ?? (body === undefined ? "GET" : "POST"),
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    if (res.status === 401 && path !== "login")
      window.dispatchEvent(new Event("ledger-unauthorized"));
    const data = await res.json().catch(() => ({ error: "Request failed" }));
    throw new Error(data.error || "Request failed");
  }
  return res.json();
}
