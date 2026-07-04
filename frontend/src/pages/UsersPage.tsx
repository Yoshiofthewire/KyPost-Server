import { FormEvent, useEffect, useState } from "react";
import { toErrorMessage } from "../api/client";
import {
  createUser,
  deactivateUser,
  listUsers,
  reactivateUser,
  resetUserPassword,
  setUserRole,
  type ManagedUser
} from "../api/users";
import { useAuth, type Role } from "../auth";

export function UsersPage() {
  const auth = useAuth();
  const [users, setUsers] = useState<ManagedUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [status, setStatus] = useState("");
  const [busyId, setBusyId] = useState("");

  const [newUsername, setNewUsername] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [newRole, setNewRole] = useState<Role>("user");
  const [createBusy, setCreateBusy] = useState(false);

  const statusTone = status.toLowerCase().includes("failed") ? "notice notice-error" : "notice notice-success";

  async function refresh() {
    try {
      const next = await listUsers();
      setUsers(next);
    } catch (error: unknown) {
      setStatus(`Failed to load users: ${toErrorMessage(error, "unknown error")}`);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void refresh();
  }, []);

  async function submitCreate(e: FormEvent) {
    e.preventDefault();
    if (!newUsername.trim() || !newPassword.trim()) {
      setStatus("Failed: username and a temporary password are required.");
      return;
    }
    setCreateBusy(true);
    setStatus("");
    try {
      const created = await createUser(newUsername.trim(), newPassword, newRole);
      setNewUsername("");
      setNewPassword("");
      setNewRole("user");
      setStatus(`User ${created.username} created. They must change the temporary password on first login.`);
      await refresh();
    } catch (error: unknown) {
      setStatus(`Failed to create user: ${toErrorMessage(error, "unknown error")}`);
    } finally {
      setCreateBusy(false);
    }
  }

  async function withRowBusy(user: ManagedUser, action: () => Promise<void>) {
    setBusyId(user.id);
    setStatus("");
    try {
      await action();
      await refresh();
    } catch (error: unknown) {
      setStatus(`Failed: ${toErrorMessage(error, "unknown error")}`);
    } finally {
      setBusyId("");
    }
  }

  function toggleRole(user: ManagedUser) {
    const nextRole: Role = user.role === "admin" ? "user" : "admin";
    void withRowBusy(user, async () => {
      await setUserRole(user.id, nextRole);
      setStatus(`${user.username} is now ${nextRole === "admin" ? "an admin" : "a standard user"}.`);
    });
  }

  function toggleActive(user: ManagedUser) {
    void withRowBusy(user, async () => {
      if (user.active) {
        await deactivateUser(user.id);
        setStatus(`${user.username} deactivated. Their data is retained and they can no longer sign in.`);
      } else {
        await reactivateUser(user.id);
        setStatus(`${user.username} reactivated.`);
      }
    });
  }

  function resetPassword(user: ManagedUser) {
    const password = window.prompt(`New temporary password for ${user.username}:`);
    if (!password || !password.trim()) {
      return;
    }
    void withRowBusy(user, async () => {
      await resetUserPassword(user.id, password);
      setStatus(`Password reset for ${user.username}. They must change it on next login.`);
    });
  }

  return (
    <section className="panel">
      <h2>Manage Users</h2>
      <p>Create accounts, adjust roles, reset passwords, and deactivate users. Each user connects their own mailbox and manages their own devices and tuning.</p>

      <form onSubmit={submitCreate} className="auth-form">
        <h3>Create User</h3>
        <label>
          <div>Username</div>
          <input value={newUsername} onChange={(e) => setNewUsername(e.target.value)} autoComplete="off" />
        </label>
        <label>
          <div>Temporary Password</div>
          <input
            type="password"
            value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
            autoComplete="new-password"
            placeholder="User must change this on first login"
          />
        </label>
        <label>
          <div>Role</div>
          <select value={newRole} onChange={(e) => setNewRole(e.target.value as Role)}>
            <option value="user">user</option>
            <option value="admin">admin</option>
          </select>
        </label>
        <button type="submit" disabled={createBusy}>
          {createBusy ? "Creating..." : "Create User"}
        </button>
      </form>

      <h3>Users</h3>
      {loading ? <p>Loading users...</p> : null}
      {!loading && users.length === 0 ? <p>No users found.</p> : null}
      {!loading && users.length > 0 ? (
        <table className="users-table">
          <thead>
            <tr>
              <th>Username</th>
              <th>Role</th>
              <th>Status</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {users.map((user) => {
              const isSelf = user.id === auth.userId;
              const busy = busyId === user.id;
              return (
                <tr key={user.id}>
                  <td>
                    {user.username}
                    {isSelf ? " (you)" : ""}
                    {user.mustChangePassword ? <span title="Must change password on next login"> *</span> : null}
                  </td>
                  <td>{user.role}</td>
                  <td>{user.active ? "active" : "deactivated"}</td>
                  <td>
                    <button type="button" onClick={() => toggleRole(user)} disabled={busy}>
                      {user.role === "admin" ? "Make User" : "Make Admin"}
                    </button>{" "}
                    <button type="button" onClick={() => resetPassword(user)} disabled={busy}>
                      Reset Password
                    </button>{" "}
                    <button type="button" onClick={() => toggleActive(user)} disabled={busy}>
                      {user.active ? "Deactivate" : "Reactivate"}
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      ) : null}
      <p className="config-muted">* password change required on next login</p>

      {status ? <p className={statusTone}>{status}</p> : null}
    </section>
  );
}
