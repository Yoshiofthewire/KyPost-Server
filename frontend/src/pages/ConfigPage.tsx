import { ChangeEvent, useEffect, useMemo, useState } from "react";
import { deleteJSON, getJSON, postFormData, postJSON, putJSON } from "../api/client";

type AppConfig = {
  timezone: string;
  logLevel: string;
  scan: { intervalSeconds: number };
  rateLimits: { perMinute: number; perHour: number };
  labels: { allowlist: string[] };
  llama: { baseUrl: string; apiKey: string; classifyPath: string };
};

type LabelsResponse = {
  configured: string[];
  imap: string[];
};

type LlamaAuthStatus = {
  exists: boolean;
  path: string;
  size?: number;
  modifiedAt?: string;
  localEnabled: boolean;
};

type LlamaAuthUploadResponse = {
  ok: boolean;
  path: string;
  filename: string;
  restartOk?: boolean;
  restartError?: string;
};

type IMAPConfigStatus = {
  configured: boolean;
  path?: string;
  keyPath?: string;
  host?: string;
  port?: number;
  username?: string;
  mailbox?: string;
  updatedAt?: string;
  encryptedAtRest?: boolean;
};

type IMAPForm = {
  host: string;
  port: number;
  username: string;
  password: string;
  mailbox: string;
};

function normalizeConfig(input: Partial<AppConfig>): AppConfig {
  return {
    timezone: input.timezone ?? "UTC",
    logLevel: input.logLevel ?? "info",
    scan: {
      intervalSeconds: input.scan?.intervalSeconds ?? 90
    },
    rateLimits: {
      perMinute: input.rateLimits?.perMinute ?? 10,
      perHour: input.rateLimits?.perHour ?? 20
    },
    labels: {
      allowlist: input.labels?.allowlist ?? []
    },
    llama: {
      baseUrl: input.llama?.baseUrl ?? "",
      apiKey: input.llama?.apiKey ?? "",
      classifyPath: input.llama?.classifyPath ?? "/"
    }
  };
}

function uniqueLabels(labels: string[]): string[] {
  return Array.from(new Set(labels.map((label) => label.trim()).filter(Boolean)));
}

function labelsToText(labels: string[]): string {
  return labels.join("\n");
}

function textToLabels(raw: string): string[] {
  return uniqueLabels(raw.split(/\r?\n/));
}

export function ConfigPage() {
  const testPrompt = "Email Address: test@example.com Subject Line: Llama connectivity test Return only the label Updates";

  const [cfg, setCfg] = useState<AppConfig | null>(null);
  const [allowlistText, setAllowlistText] = useState("");
  const [labelsFromImap, setLabelsFromImap] = useState<string[]>([]);
  const [configStatus, setConfigStatus] = useState("");

  const [imapStatus, setImapStatus] = useState<IMAPConfigStatus | null>(null);
  const [imapForm, setImapForm] = useState<IMAPForm>({
    host: "",
    port: 993,
    username: "",
    password: "",
    mailbox: "INBOX"
  });
  const [imapMessage, setImapMessage] = useState("");
  const [imapBusy, setImapBusy] = useState(false);

  const [llamaAuthStatus, setLlamaAuthStatus] = useState<LlamaAuthStatus | null>(null);
  const [llamaAuthFile, setLlamaAuthFile] = useState<File | null>(null);
  const [llamaAuthMessage, setLlamaAuthMessage] = useState("");
  const [llamaAuthBusy, setLlamaAuthBusy] = useState(false);

  const [llamaTestBusy, setLlamaTestBusy] = useState(false);
  const [llamaTestResult, setLlamaTestResult] = useState("");

  const effectiveAllowlist = useMemo(() => {
    const cfgLabels = textToLabels(allowlistText);
    return uniqueLabels([...cfgLabels]);
  }, [allowlistText]);

  async function refreshLabels() {
    const labelsData = await getJSON<LabelsResponse>("/api/labels");
    setLabelsFromImap(uniqueLabels(labelsData.imap ?? []));
  }

  async function refreshIMAPStatus() {
    const status = await getJSON<IMAPConfigStatus>("/api/imap/config");
    setImapStatus(status);
    if (status.configured) {
      setImapForm((prev) => ({
        host: status.host ?? prev.host,
        port: status.port ?? prev.port,
        username: status.username ?? prev.username,
        password: "",
        mailbox: status.mailbox ?? prev.mailbox
      }));
    }
  }

  async function refreshLlamaAuthStatus() {
    const status = await getJSON<LlamaAuthStatus>("/api/llama/auth");
    setLlamaAuthStatus(status);
  }

  useEffect(() => {
    let cancelled = false;

    const load = async () => {
      try {
        const nextConfig = await getJSON<AppConfig>("/api/config");
        if (cancelled) {
          return;
        }
        const normalized = normalizeConfig(nextConfig);
        setCfg(normalized);
        setAllowlistText(labelsToText(normalized.labels.allowlist));
      } catch {
        if (!cancelled) {
          setConfigStatus("Failed to load configuration data.");
        }
        return;
      }

      // Load secondary panels independently so one failure does not block the entire page.
      await Promise.all([
        refreshLabels().catch(() => undefined),
        refreshIMAPStatus().catch(() => undefined),
        refreshLlamaAuthStatus().catch(() => undefined)
      ]);
    };

    load();
    return () => {
      cancelled = true;
    };
  }, []);

  if (!cfg) {
    return (
      <section className="panel">
        <h2>Configuration</h2>
        <p>{configStatus || "Loading configuration..."}</p>
      </section>
    );
  }

  async function saveConfig() {
    const next: AppConfig = {
      ...cfg,
      labels: {
        ...cfg.labels,
        allowlist: effectiveAllowlist
      }
    };

    try {
      await putJSON<{ ok: boolean }>("/api/config", next);
      setCfg(next);
      setConfigStatus("Configuration saved.");
    } catch {
      setConfigStatus("Failed to save configuration.");
    }
  }

  function applyImapLabelsToAllowlist() {
    const merged = uniqueLabels([...effectiveAllowlist, ...labelsFromImap]);
    setAllowlistText(labelsToText(merged));
    setConfigStatus("Merged discovered IMAP labels into allowlist (not yet saved).");
  }

  async function saveIMAPConfig() {
    setImapBusy(true);
    setImapMessage("");
    try {
      const result = await postJSON<IMAPConfigStatus>("/api/imap/config", imapForm);
      setImapStatus(result);
      setImapForm((prev) => ({ ...prev, password: "" }));
      setImapMessage("IMAP configuration saved.");
      await refreshLabels();
    } catch (error: unknown) {
      const message = error instanceof Error ? error.message : "unknown error";
      setImapMessage(`Failed to save IMAP config: ${message}`);
    } finally {
      setImapBusy(false);
    }
  }

  async function testIMAPConfig() {
    setImapBusy(true);
    setImapMessage("");
    try {
      const result = await postJSON<{ ok: boolean; error?: string; host?: string; port?: number; mailbox?: string }>(
        "/api/imap/test",
        imapForm
      );
      if (result.ok) {
        setImapMessage(`IMAP test passed (${result.host}:${result.port} ${result.mailbox}).`);
      } else {
        setImapMessage(`IMAP test failed: ${result.error ?? "unknown error"}`);
      }
    } catch (error: unknown) {
      const message = error instanceof Error ? error.message : "unknown error";
      setImapMessage(`IMAP test failed: ${message}`);
    } finally {
      setImapBusy(false);
    }
  }

  async function deleteIMAPConfig() {
    setImapBusy(true);
    setImapMessage("");
    try {
      await deleteJSON<{ ok: boolean; configured: boolean }>("/api/imap/config");
      setImapStatus({ configured: false });
      setImapForm({ host: "", port: 993, username: "", password: "", mailbox: "INBOX" });
      setImapMessage("Stored IMAP configuration removed.");
    } catch (error: unknown) {
      const message = error instanceof Error ? error.message : "unknown error";
      setImapMessage(`Failed to delete IMAP config: ${message}`);
    } finally {
      setImapBusy(false);
    }
  }

  async function uploadLlamaAuth() {
    if (!llamaAuthFile) {
      setLlamaAuthMessage("Select a JSON auth file first.");
      return;
    }
    setLlamaAuthBusy(true);
    setLlamaAuthMessage("");
    try {
      const form = new FormData();
      form.append("authFile", llamaAuthFile);
      const result = await postFormData<LlamaAuthUploadResponse>("/api/llama/auth", form);
      if (result.restartOk === false) {
        setLlamaAuthMessage(`Auth saved, but Llama restart needs attention: ${result.restartError ?? "unknown error"}`);
      } else {
        setLlamaAuthMessage("Llama auth uploaded and runtime reloaded.");
      }
      setLlamaAuthFile(null);
      await refreshLlamaAuthStatus();
    } catch (error: unknown) {
      const message = error instanceof Error ? error.message : "unknown error";
      setLlamaAuthMessage(`Failed to upload Llama auth: ${message}`);
    } finally {
      setLlamaAuthBusy(false);
    }
  }

  async function runLlamaTest() {
    setLlamaTestBusy(true);
    setLlamaTestResult("");
    try {
      const result = await postJSON<{ ok: boolean; response?: string; error?: string; baseUrl?: string; path?: string }>(
        "/api/llama/test",
        { prompt: testPrompt }
      );
      if (!result.ok) {
        setLlamaTestResult(`Llama test failed: ${result.error ?? "unknown error"}`);
      } else {
        setLlamaTestResult(
          `Llama test passed\nBase URL: ${result.baseUrl ?? ""}\nPath: ${result.path ?? ""}\nResponse: ${result.response ?? ""}`
        );
      }
    } catch (error: unknown) {
      const message = error instanceof Error ? error.message : "unknown error";
      setLlamaTestResult(`Llama test failed: ${message}`);
    } finally {
      setLlamaTestBusy(false);
    }
  }

  function updateConfig<K extends keyof AppConfig>(key: K, value: AppConfig[K]) {
    setCfg((prev) => (prev ? { ...prev, [key]: value } : prev));
  }

  function onLlamaAuthFileChange(event: ChangeEvent<HTMLInputElement>) {
    setLlamaAuthFile(event.target.files?.[0] ?? null);
  }

  return (
    <section className="panel">
      <h2>Configuration</h2>
      <p>Manage app settings, IMAP credentials, labels, and Llama connectivity.</p>

      <hr />
      <h3>Application</h3>
      <label>
        <div>Timezone</div>
        <input value={cfg.timezone} onChange={(event) => updateConfig("timezone", event.target.value)} />
      </label>
      <label>
        <div>Log Level</div>
        <input value={cfg.logLevel} onChange={(event) => updateConfig("logLevel", event.target.value)} />
      </label>
      <label>
        <div>Scan Interval (seconds)</div>
        <input
          type="number"
          value={cfg.scan.intervalSeconds}
          onChange={(event) => updateConfig("scan", { intervalSeconds: Number(event.target.value) || 0 })}
        />
      </label>
      <label>
        <div>Rate Limit Per Minute</div>
        <input
          type="number"
          value={cfg.rateLimits.perMinute}
          onChange={(event) => updateConfig("rateLimits", { ...cfg.rateLimits, perMinute: Number(event.target.value) || 0 })}
        />
      </label>
      <label>
        <div>Rate Limit Per Hour</div>
        <input
          type="number"
          value={cfg.rateLimits.perHour}
          onChange={(event) => updateConfig("rateLimits", { ...cfg.rateLimits, perHour: Number(event.target.value) || 0 })}
        />
      </label>

      <hr />
      <h3>Llama</h3>
      <label>
        <div>Base URL</div>
        <input value={cfg.llama.baseUrl} onChange={(event) => updateConfig("llama", { ...cfg.llama, baseUrl: event.target.value })} />
      </label>
      <label>
        <div>API Key</div>
        <input
          type="password"
          value={cfg.llama.apiKey}
          onChange={(event) => updateConfig("llama", { ...cfg.llama, apiKey: event.target.value })}
        />
      </label>
      <label>
        <div>Classify Path</div>
        <input
          value={cfg.llama.classifyPath}
          onChange={(event) => updateConfig("llama", { ...cfg.llama, classifyPath: event.target.value })}
        />
      </label>

      <h4>Llama Auth Upload</h4>
      <label>
        <div>Auth File (JSON)</div>
        <input type="file" accept="application/json,.json" onChange={onLlamaAuthFileChange} />
      </label>
      <button type="button" onClick={uploadLlamaAuth} disabled={llamaAuthBusy}>
        {llamaAuthBusy ? "Uploading..." : "Upload Llama Auth"}
      </button>
      {llamaAuthStatus ? (
        <div style={{ border: "1px solid var(--line)", borderRadius: 6, padding: 10, marginTop: 10 }}>
          <p>Configured: {llamaAuthStatus.exists ? "Yes" : "No"}</p>
          <p>Path: {llamaAuthStatus.path}</p>
          {llamaAuthStatus.modifiedAt ? <p>Updated: {llamaAuthStatus.modifiedAt}</p> : null}
          <p>Local Enabled: {llamaAuthStatus.localEnabled ? "Yes" : "No"}</p>
        </div>
      ) : null}
      {llamaAuthMessage ? <p>{llamaAuthMessage}</p> : null}

      <button type="button" onClick={runLlamaTest} disabled={llamaTestBusy}>
        {llamaTestBusy ? "Testing..." : "Run Llama Test"}
      </button>
      {llamaTestResult ? <pre>{llamaTestResult}</pre> : null}

      <hr />
      <h3>IMAP</h3>
      <p>Saved IMAP config is encrypted at rest.</p>
      <label>
        <div>Host</div>
        <input value={imapForm.host} onChange={(event) => setImapForm((prev) => ({ ...prev, host: event.target.value }))} />
      </label>
      <label>
        <div>Port</div>
        <input
          type="number"
          value={imapForm.port}
          onChange={(event) => setImapForm((prev) => ({ ...prev, port: Number(event.target.value) || 993 }))}
        />
      </label>
      <label>
        <div>Username</div>
        <input value={imapForm.username} onChange={(event) => setImapForm((prev) => ({ ...prev, username: event.target.value }))} />
      </label>
      <label>
        <div>Password or App Password</div>
        <input
          type="password"
          value={imapForm.password}
          onChange={(event) => setImapForm((prev) => ({ ...prev, password: event.target.value }))}
          placeholder="Required when saving changes"
        />
      </label>
      <label>
        <div>Mailbox</div>
        <input value={imapForm.mailbox} onChange={(event) => setImapForm((prev) => ({ ...prev, mailbox: event.target.value }))} />
      </label>
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
        <button type="button" onClick={saveIMAPConfig} disabled={imapBusy}>
          {imapBusy ? "Saving..." : "Save IMAP Config"}
        </button>
        <button type="button" onClick={testIMAPConfig} disabled={imapBusy}>
          {imapBusy ? "Testing..." : "Test IMAP"}
        </button>
        <button type="button" onClick={deleteIMAPConfig} disabled={imapBusy}>
          Delete Stored IMAP Config
        </button>
      </div>
      {imapStatus ? (
        <div style={{ border: "1px solid var(--line)", borderRadius: 6, padding: 10, marginTop: 10 }}>
          <p>Configured: {imapStatus.configured ? "Yes" : "No"}</p>
          {imapStatus.path ? <p>Config Path: {imapStatus.path}</p> : null}
          {imapStatus.keyPath ? <p>Key Path: {imapStatus.keyPath}</p> : null}
          {imapStatus.host ? <p>Host: {imapStatus.host}</p> : null}
          {imapStatus.port ? <p>Port: {imapStatus.port}</p> : null}
          {imapStatus.username ? <p>Username: {imapStatus.username}</p> : null}
          {imapStatus.mailbox ? <p>Mailbox: {imapStatus.mailbox}</p> : null}
          {imapStatus.updatedAt ? <p>Updated: {imapStatus.updatedAt}</p> : null}
        </div>
      ) : null}
      {imapMessage ? <p>{imapMessage}</p> : null}

      <hr />
      <h3>Label Allowlist</h3>
      <p>One label per line. These names must match mailbox keywords you want to apply.</p>
      <label>
        <div>Allowlist</div>
        <textarea rows={10} value={allowlistText} onChange={(event) => setAllowlistText(event.target.value)} style={{ width: "100%" }} />
      </label>
      <button type="button" onClick={applyImapLabelsToAllowlist}>Merge IMAP Labels</button>
      <button type="button" onClick={saveConfig}>Save Configuration</button>
      {labelsFromImap.length > 0 ? <p>Discovered IMAP labels: {labelsFromImap.join(", ")}</p> : <p>No IMAP labels discovered yet.</p>}

      {configStatus ? <p>{configStatus}</p> : null}
    </section>
  );
}
