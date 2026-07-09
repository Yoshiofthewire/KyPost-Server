import { FormEvent, useEffect, useState } from "react";
import QRCode from "qrcode";
import { getJSON, postJSON, putJSON, toErrorMessage } from "../api/client";

type ApproverDevice = {
  deviceId: string;
  deviceName?: string;
  platform?: string;
  approver: boolean;
};

type MfaStatus = {
  totpEnabled: boolean;
  recoveryCodesRemaining: number;
  pushMfaEnabled: boolean;
  approverDevices: ApproverDevice[];
};

type SetupResponse = {
  secret: string;
  otpauthUri: string;
};

type ConfirmResponse = {
  ok: boolean;
  recoveryCodes: string[];
};

export function SecurityPage() {
  const [status, setStatus] = useState<MfaStatus | null>(null);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);

  // Enrollment state.
  const [setup, setSetup] = useState<SetupResponse | null>(null);
  const [qrDataUrl, setQrDataUrl] = useState("");
  const [confirmCode, setConfirmCode] = useState("");

  // Recovery-code display (shown once after confirm or regenerate).
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [savedAcknowledged, setSavedAcknowledged] = useState(false);

  // Password-confirm modals.
  const [disablePassword, setDisablePassword] = useState("");
  const [showDisable, setShowDisable] = useState(false);
  const [regeneratePassword, setRegeneratePassword] = useState("");
  const [showRegenerate, setShowRegenerate] = useState(false);

  async function refreshStatus() {
    try {
      const res = await getJSON<MfaStatus>("/api/mfa/status");
      setStatus(res);
    } catch (err) {
      setMessage(toErrorMessage(err, "Failed to load security status."));
    }
  }

  useEffect(() => {
    void refreshStatus();
  }, []);

  useEffect(() => {
    let cancelled = false;
    if (!setup?.otpauthUri) {
      setQrDataUrl("");
      return;
    }
    QRCode.toDataURL(setup.otpauthUri, { errorCorrectionLevel: "M", margin: 2, width: 220 })
      .then((dataUrl) => {
        if (!cancelled) {
          setQrDataUrl(dataUrl);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setQrDataUrl("");
        }
      });
    return () => {
      cancelled = true;
    };
  }, [setup]);

  async function beginSetup() {
    setBusy(true);
    setMessage("");
    setRecoveryCodes([]);
    setSavedAcknowledged(false);
    try {
      const res = await postJSON<SetupResponse>("/api/mfa/totp/setup", {});
      setSetup(res);
      setConfirmCode("");
    } catch (err) {
      setMessage(toErrorMessage(err, "Failed to start enrollment."));
    } finally {
      setBusy(false);
    }
  }

  async function submitConfirm(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setMessage("");
    try {
      const res = await postJSON<ConfirmResponse>("/api/mfa/totp/confirm", {
        code: confirmCode.trim()
      });
      setRecoveryCodes(res.recoveryCodes);
      setSavedAcknowledged(false);
      setSetup(null);
      setConfirmCode("");
      await refreshStatus();
    } catch (err) {
      setMessage(toErrorMessage(err, "Invalid code. Try again."));
    } finally {
      setBusy(false);
    }
  }

  async function submitDisable(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setMessage("");
    try {
      await postJSON<{ ok: boolean }>("/api/mfa/totp/disable", { password: disablePassword });
      setShowDisable(false);
      setDisablePassword("");
      setRecoveryCodes([]);
      setMessage("Two-factor authentication disabled.");
      await refreshStatus();
    } catch (err) {
      setMessage(toErrorMessage(err, "Failed to disable. Check your password."));
    } finally {
      setBusy(false);
    }
  }

  async function submitRegenerate(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setMessage("");
    try {
      const res = await postJSON<ConfirmResponse>("/api/mfa/recovery-codes/regenerate", {
        password: regeneratePassword
      });
      setRecoveryCodes(res.recoveryCodes);
      setSavedAcknowledged(false);
      setShowRegenerate(false);
      setRegeneratePassword("");
      await refreshStatus();
    } catch (err) {
      setMessage(toErrorMessage(err, "Failed to regenerate. Check your password."));
    } finally {
      setBusy(false);
    }
  }

  async function togglePush(enabled: boolean) {
    setBusy(true);
    setMessage("");
    try {
      await putJSON<{ ok: boolean; pushMfaEnabled: boolean }>("/api/mfa/push/enabled", { enabled });
      await refreshStatus();
    } catch (err) {
      setMessage(toErrorMessage(err, "Failed to update push approval."));
    } finally {
      setBusy(false);
    }
  }

  async function toggleApprover(deviceId: string, approver: boolean) {
    setBusy(true);
    setMessage("");
    try {
      await putJSON<{ ok: boolean }>(
        `/api/notifications/native/devices/${encodeURIComponent(deviceId)}/mfa`,
        { approver }
      );
      await refreshStatus();
    } catch (err) {
      setMessage(toErrorMessage(err, "Failed to update device."));
    } finally {
      setBusy(false);
    }
  }

  function copyRecoveryCodes() {
    void navigator.clipboard?.writeText(recoveryCodes.join("\n"));
  }

  const showRecoveryPanel = recoveryCodes.length > 0;

  return (
    <section className="panel">
      <h2>Security</h2>
      <p>Protect your account with an authenticator app (TOTP) as a second factor.</p>

      {message ? <p>{message}</p> : null}

      {showRecoveryPanel ? (
        <div className="auth-form">
          <h3>Save your recovery codes</h3>
          <p>
            Store these one-time recovery codes somewhere safe. Each works once if you lose access to
            your authenticator. They will not be shown again.
          </p>
          <ul>
            {recoveryCodes.map((code) => (
              <li key={code}>
                <code>{code}</code>
              </li>
            ))}
          </ul>
          <button type="button" onClick={copyRecoveryCodes}>
            Copy codes
          </button>
          <label>
            <input
              type="checkbox"
              checked={savedAcknowledged}
              onChange={(e) => setSavedAcknowledged(e.target.checked)}
            />{" "}
            I have saved these recovery codes
          </label>
          <button type="button" disabled={!savedAcknowledged} onClick={() => setRecoveryCodes([])}>
            Done
          </button>
        </div>
      ) : status?.totpEnabled ? (
        <div className="auth-form">
          <p>Two-factor authentication is enabled.</p>
          <p>Recovery codes remaining: {status.recoveryCodesRemaining}</p>
          <button type="button" onClick={() => setShowRegenerate(true)}>
            Regenerate recovery codes
          </button>
          <button type="button" onClick={() => setShowDisable(true)}>
            Disable two-factor auth
          </button>

          {showRegenerate ? (
            <form onSubmit={submitRegenerate} className="auth-form">
              <h3>Confirm your password</h3>
              <label>
                <div>Password</div>
                <input
                  type="password"
                  value={regeneratePassword}
                  onChange={(e) => setRegeneratePassword(e.target.value)}
                  autoComplete="current-password"
                />
              </label>
              <button type="submit" disabled={busy || regeneratePassword === ""}>
                {busy ? "Working..." : "Regenerate"}
              </button>
              <button type="button" className="nav-link-button" onClick={() => setShowRegenerate(false)}>
                Cancel
              </button>
            </form>
          ) : null}

          {showDisable ? (
            <form onSubmit={submitDisable} className="auth-form">
              <h3>Confirm your password</h3>
              <label>
                <div>Password</div>
                <input
                  type="password"
                  value={disablePassword}
                  onChange={(e) => setDisablePassword(e.target.value)}
                  autoComplete="current-password"
                />
              </label>
              <button type="submit" disabled={busy || disablePassword === ""}>
                {busy ? "Working..." : "Disable"}
              </button>
              <button type="button" className="nav-link-button" onClick={() => setShowDisable(false)}>
                Cancel
              </button>
            </form>
          ) : null}
        </div>
      ) : setup ? (
        <form onSubmit={submitConfirm} className="auth-form">
          <h3>Scan this QR code</h3>
          <p>Scan with your authenticator app, or enter the key manually.</p>
          {qrDataUrl ? (
            <img src={qrDataUrl} alt="TOTP enrollment QR code" width={220} height={220} />
          ) : null}
          <p>
            Manual entry key: <code>{setup.secret}</code>
          </p>
          <label>
            <div>Enter the 6-digit code to confirm</div>
            <input
              value={confirmCode}
              onChange={(e) => setConfirmCode(e.target.value.replace(/\D/g, "").slice(0, 6))}
              inputMode="numeric"
              autoComplete="one-time-code"
              placeholder="123456"
            />
          </label>
          <button type="submit" disabled={busy || confirmCode.trim().length !== 6}>
            {busy ? "Confirming..." : "Confirm and enable"}
          </button>
          <button type="button" className="nav-link-button" onClick={() => setSetup(null)}>
            Cancel
          </button>
        </form>
      ) : (
        <div className="auth-form">
          <p>Two-factor authentication is not enabled.</p>
          <button type="button" disabled={busy} onClick={() => void beginSetup()}>
            {busy ? "Starting..." : "Enable 2FA"}
          </button>
        </div>
      )}

      <div className="auth-form">
        <h3>Push approval (2FA)</h3>
        {!status?.totpEnabled ? (
          <p>
            Enable an authenticator app (TOTP) above first. Push approval always keeps TOTP as a
            fallback, so it can only be turned on once TOTP is active.
          </p>
        ) : (
          <>
            <p>
              Approve sign-ins from a paired device. You can still use your authenticator code at any
              time.
            </p>
            <label>
              <input
                type="checkbox"
                checked={Boolean(status?.pushMfaEnabled)}
                disabled={busy}
                onChange={(e) => void togglePush(e.target.checked)}
              />{" "}
              Enable push approval
            </label>
            {status && status.approverDevices.length > 0 ? (
              <ul>
                {status.approverDevices.map((device) => (
                  <li key={device.deviceId}>
                    <label>
                      <input
                        type="checkbox"
                        checked={device.approver}
                        disabled={busy}
                        onChange={(e) => void toggleApprover(device.deviceId, e.target.checked)}
                      />{" "}
                      {device.deviceName?.trim() || device.platform || device.deviceId} — may approve
                      sign-ins
                    </label>
                  </li>
                ))}
              </ul>
            ) : (
              <p>No paired devices yet. Pair a device on the Notifications page to use push approval.</p>
            )}
          </>
        )}
      </div>
    </section>
  );
}
