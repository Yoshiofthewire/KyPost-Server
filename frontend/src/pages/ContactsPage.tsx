import { FormEvent, useEffect, useState } from "react";
import { toErrorMessage } from "../api/client";
import {
  createContact,
  deleteContact,
  generateDAVPassword,
  getDAVPasswordStatus,
  listContacts,
  revokeDAVPassword,
  updateContact,
  type Contact,
  type ContactInput,
  type DAVPasswordStatus
} from "../api/contacts";
import { useAuth } from "../auth";

type FormState = {
  fn: string;
  givenName: string;
  familyName: string;
  org: string;
  email: string;
  phone: string;
  notes: string;
};

const emptyFormState: FormState = {
  fn: "",
  givenName: "",
  familyName: "",
  org: "",
  email: "",
  phone: "",
  notes: ""
};

function contactToFormState(contact: Contact): FormState {
  return {
    fn: contact.fn,
    givenName: contact.givenName ?? "",
    familyName: contact.familyName ?? "",
    org: contact.org ?? "",
    email: contact.emails?.[0]?.value ?? "",
    phone: contact.phones?.[0]?.value ?? "",
    notes: contact.notes ?? ""
  };
}

function formStateToInput(form: FormState): ContactInput {
  const input: ContactInput = {
    fn: form.fn.trim(),
    givenName: form.givenName.trim() || undefined,
    familyName: form.familyName.trim() || undefined,
    org: form.org.trim() || undefined,
    notes: form.notes.trim() || undefined
  };
  if (form.email.trim()) {
    input.emails = [{ value: form.email.trim() }];
  }
  if (form.phone.trim()) {
    input.phones = [{ value: form.phone.trim() }];
  }
  return input;
}

function contactDisplayLine(contact: Contact): string {
  return contact.emails?.[0]?.value ?? contact.phones?.[0]?.value ?? "";
}

export function ContactsPage() {
  const auth = useAuth();
  const [contacts, setContacts] = useState<Contact[]>([]);
  const [loading, setLoading] = useState(true);
  const [status, setStatus] = useState("");
  const [busyId, setBusyId] = useState("");

  const [form, setForm] = useState<FormState>(emptyFormState);
  const [editingUid, setEditingUid] = useState("");
  const [saving, setSaving] = useState(false);

  const [davStatus, setDavStatus] = useState<DAVPasswordStatus | null>(null);
  const [davBusy, setDavBusy] = useState(false);
  const [revealedPassword, setRevealedPassword] = useState("");
  const [copyStatus, setCopyStatus] = useState("");

  const statusTone = status.toLowerCase().includes("failed") ? "notice notice-error" : "notice notice-success";
  const davURL = auth.username ? `${window.location.origin}/dav/${encodeURIComponent(auth.username)}/contacts/` : "";

  async function refresh() {
    try {
      const next = await listContacts();
      next.sort((a, b) => a.fn.localeCompare(b.fn));
      setContacts(next);
    } catch (error: unknown) {
      setStatus(`Failed to load contacts: ${toErrorMessage(error, "unknown error")}`);
    } finally {
      setLoading(false);
    }
  }

  async function refreshDavStatus() {
    try {
      setDavStatus(await getDAVPasswordStatus());
    } catch {
      // Non-fatal: the CardDAV access card just shows nothing configured.
    }
  }

  useEffect(() => {
    void refresh();
    void refreshDavStatus();
  }, []);

  function startCreate() {
    setEditingUid("");
    setForm(emptyFormState);
  }

  function startEdit(contact: Contact) {
    setEditingUid(contact.uid);
    setForm(contactToFormState(contact));
  }

  async function submitForm(e: FormEvent) {
    e.preventDefault();
    if (!form.fn.trim()) {
      setStatus("Failed: full name is required.");
      return;
    }
    setSaving(true);
    setStatus("");
    try {
      const input = formStateToInput(form);
      if (editingUid) {
        await updateContact(editingUid, input);
        setStatus(`${input.fn} updated.`);
      } else {
        await createContact(input);
        setStatus(`${input.fn} added.`);
      }
      startCreate();
      await refresh();
    } catch (error: unknown) {
      setStatus(`Failed to save contact: ${toErrorMessage(error, "unknown error")}`);
    } finally {
      setSaving(false);
    }
  }

  async function removeContact(contact: Contact) {
    if (!window.confirm(`Delete ${contact.fn}?`)) {
      return;
    }
    setBusyId(contact.uid);
    setStatus("");
    try {
      await deleteContact(contact.uid);
      setStatus(`${contact.fn} deleted.`);
      if (editingUid === contact.uid) {
        startCreate();
      }
      await refresh();
    } catch (error: unknown) {
      setStatus(`Failed to delete contact: ${toErrorMessage(error, "unknown error")}`);
    } finally {
      setBusyId("");
    }
  }

  async function generatePassword() {
    setDavBusy(true);
    setCopyStatus("");
    try {
      const generated = await generateDAVPassword();
      setRevealedPassword(generated.password);
      await refreshDavStatus();
    } catch (error: unknown) {
      setStatus(`Failed to generate CardDAV password: ${toErrorMessage(error, "unknown error")}`);
    } finally {
      setDavBusy(false);
    }
  }

  async function revokePassword() {
    if (
      !window.confirm(
        "Revoke the CardDAV app password? Any connected CardDAV client will stop syncing until you generate a new one."
      )
    ) {
      return;
    }
    setDavBusy(true);
    setCopyStatus("");
    try {
      await revokeDAVPassword();
      setRevealedPassword("");
      await refreshDavStatus();
    } catch (error: unknown) {
      setStatus(`Failed to revoke CardDAV password: ${toErrorMessage(error, "unknown error")}`);
    } finally {
      setDavBusy(false);
    }
  }

  function copyPassword() {
    if (!revealedPassword || !navigator.clipboard?.writeText) {
      return;
    }
    void navigator.clipboard.writeText(revealedPassword).then(
      () => setCopyStatus("Copied to clipboard."),
      () => setCopyStatus("Could not copy automatically — copy it manually.")
    );
  }

  return (
    <section className="panel contacts-page">
      <header className="contacts-header">
        <div>
          <h2>Contacts</h2>
          <p>
            Your local address book. Sync it to a phone or computer over CardDAV, or let the Llama Labels mobile app
            pull it down automatically once paired.
          </p>
        </div>
        {!loading && contacts.length > 0 ? (
          <div className="contacts-stats">
            <span className="contacts-stat">
              <strong>{contacts.length}</strong> contact{contacts.length === 1 ? "" : "s"}
            </span>
          </div>
        ) : null}
      </header>

      <div className="contacts-layout">
        <form onSubmit={submitForm} className="contacts-card contacts-form-card">
          <h3>{editingUid ? "Edit Contact" : "Add Contact"}</h3>
          <label>
            <div>Full Name</div>
            <input value={form.fn} onChange={(e) => setForm({ ...form, fn: e.target.value })} autoComplete="off" />
          </label>
          <label>
            <div>Given Name</div>
            <input
              value={form.givenName}
              onChange={(e) => setForm({ ...form, givenName: e.target.value })}
              autoComplete="off"
            />
          </label>
          <label>
            <div>Family Name</div>
            <input
              value={form.familyName}
              onChange={(e) => setForm({ ...form, familyName: e.target.value })}
              autoComplete="off"
            />
          </label>
          <label>
            <div>Organization</div>
            <input value={form.org} onChange={(e) => setForm({ ...form, org: e.target.value })} autoComplete="off" />
          </label>
          <label>
            <div>Email</div>
            <input
              type="email"
              value={form.email}
              onChange={(e) => setForm({ ...form, email: e.target.value })}
              autoComplete="off"
            />
          </label>
          <label>
            <div>Phone</div>
            <input value={form.phone} onChange={(e) => setForm({ ...form, phone: e.target.value })} autoComplete="off" />
          </label>
          <label>
            <div>Notes</div>
            <textarea value={form.notes} onChange={(e) => setForm({ ...form, notes: e.target.value })} rows={3} />
          </label>
          <div className="contacts-form-actions">
            <button type="submit" className="contacts-create-submit" disabled={saving}>
              {saving ? "Saving..." : editingUid ? "Save Changes" : "Add Contact"}
            </button>
            {editingUid ? (
              <button type="button" className="contacts-action" onClick={startCreate} disabled={saving}>
                Cancel
              </button>
            ) : null}
          </div>
        </form>

        <div className="contacts-card contacts-list-card">
          <div className="contacts-list-head">
            <h3>Address Book</h3>
            {!loading && contacts.length > 0 ? <span className="contacts-count">{contacts.length}</span> : null}
          </div>

          {loading ? <p className="contacts-muted">Loading contacts...</p> : null}
          {!loading && contacts.length === 0 ? <div className="contacts-empty">No contacts yet.</div> : null}

          {!loading && contacts.length > 0 ? (
            <div className="contacts-table-wrap">
              <table className="contacts-table">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Contact Info</th>
                    <th className="contacts-col-actions">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {contacts.map((contact) => {
                    const busy = busyId === contact.uid;
                    return (
                      <tr key={contact.uid} className={busy ? "contacts-row contacts-row-busy" : "contacts-row"}>
                        <td>
                          <div className="contacts-identity">
                            <span className="contacts-avatar" aria-hidden="true">
                              {contact.fn.slice(0, 1).toUpperCase() || "?"}
                            </span>
                            <div className="contacts-identity-text">
                              <span className="contacts-name">{contact.fn}</span>
                              {contact.org ? <span className="contacts-sub">{contact.org}</span> : null}
                            </div>
                          </div>
                        </td>
                        <td>{contactDisplayLine(contact) || <span className="contacts-muted">—</span>}</td>
                        <td className="contacts-col-actions">
                          <div className="contacts-actions">
                            <button
                              type="button"
                              className="contacts-action"
                              onClick={() => startEdit(contact)}
                              disabled={busy}
                            >
                              Edit
                            </button>
                            <button
                              type="button"
                              className="contacts-action contacts-action-danger"
                              onClick={() => void removeContact(contact)}
                              disabled={busy}
                            >
                              {busy ? "Deleting..." : "Delete"}
                            </button>
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          ) : null}
        </div>
      </div>

      <div className="contacts-card contacts-dav-card">
        <h3>CardDAV Access</h3>
        <p className="contacts-muted">
          Point a CardDAV-capable app (iOS/macOS Contacts, Nextcloud, Thunderbird, or the Llama Labels mobile app) at
          the address below using an app-specific password — never your account login password.
        </p>
        {davURL ? (
          <div className="contacts-dav-url">
            <code>{davURL}</code>
          </div>
        ) : null}
        <div className="contacts-dav-status">
          {davStatus?.configured ? (
            <span className="contacts-badge contacts-status-active">
              <span className="contacts-dot" aria-hidden="true" />
              app password configured
            </span>
          ) : (
            <span className="contacts-badge contacts-status-inactive">
              <span className="contacts-dot" aria-hidden="true" />
              no app password yet
            </span>
          )}
        </div>
        {revealedPassword ? (
          <div className="contacts-dav-reveal">
            <p className="contacts-muted">
              Copy this now — it will not be shown again. Use it as the password for the CardDAV account above.
            </p>
            <div className="contacts-dav-secret">
              <code>{revealedPassword}</code>
              <button type="button" className="contacts-action" onClick={copyPassword}>
                Copy
              </button>
            </div>
            {copyStatus ? <p className="contacts-muted">{copyStatus}</p> : null}
          </div>
        ) : null}
        <div className="contacts-dav-actions">
          <button type="button" className="contacts-action" onClick={() => void generatePassword()} disabled={davBusy}>
            {davBusy ? "Working..." : davStatus?.configured ? "Regenerate Password" : "Generate Password"}
          </button>
          {davStatus?.configured ? (
            <button
              type="button"
              className="contacts-action contacts-action-danger"
              onClick={() => void revokePassword()}
              disabled={davBusy}
            >
              Revoke
            </button>
          ) : null}
        </div>
      </div>

      {status ? <p className={statusTone}>{status}</p> : null}
    </section>
  );
}
