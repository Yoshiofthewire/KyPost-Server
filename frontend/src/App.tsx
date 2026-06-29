import { useEffect, useState } from "react";
import { Link, Navigate, Route, Routes } from "react-router-dom";
import { getJSON, postJSON } from "./api/client";
import { ConfigPage } from "./pages/ConfigPage";
import { DecisionsPage } from "./pages/DecisionsPage";
import { LoginPage } from "./pages/LoginPage";
import { LogsPage } from "./pages/LogsPage";
import { LabelsPage } from "./pages/LabelsPage";
import { ReadPage } from "./pages/ReadPage";
import { StatusPage } from "./pages/StatusPage";
import { TuningPage } from "./pages/TuningPage";

const primaryNavItems = [
  ["/read", "Inbox"]
] as const;
const settingsNavItems = [
  ["/login", "Login"],
  ["/status", "Status"],
  ["/config", "Config"],
  ["/tuning", "Tuning"],
  ["/logs", "Logs"]
] as const;

type AuthState = {
  authenticated: boolean;
  username?: string;
  mustChangePassword?: boolean;
};

export function App() {
  const [auth, setAuth] = useState<AuthState | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);

  async function refreshAuth() {
    try {
      const next = await getJSON<AuthState>("/api/auth/me");
      setAuth(next);
    } catch {
      setAuth({ authenticated: false });
    }
  }

  useEffect(() => {
    refreshAuth();
  }, []);

  async function logout() {
    try {
      await postJSON<{ ok: boolean }>("/api/auth/logout", {});
    } finally {
      setAuth({ authenticated: false });
    }
  }

  if (auth === null) {
    return (
      <div className="shell">
        <main className="content">
          <section className="panel">
            <h2>Loading</h2>
            <p>Checking session...</p>
          </section>
        </main>
      </div>
    );
  }

  function protect(element: JSX.Element) {
    if (!auth.authenticated) {
      return <Navigate to="/login" replace />;
    }
    return element;
  }

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="sidebar-logo">
          <img src="/llamalabel.png" alt="Llama Labels" style={{ width: "100%", maxWidth: 180, display: "block", margin: "0 auto 0.75rem" }} />
        </div>
        <nav>
          {primaryNavItems.map(([to, label]) => (
            <Link key={to} to={to}>
              {label}
            </Link>
          ))}

          <button
            type="button"
            className="nav-heading"
            aria-expanded={settingsOpen}
            onClick={() => setSettingsOpen((open) => !open)}
          >
            Settings {settingsOpen ? "-" : "+"}
          </button>

          {settingsOpen ? (
            <div className="nav-group">
              {settingsNavItems.map(([to, label]) => (
                <Link key={to} to={to}>
                  {to === "/login" && auth.authenticated ? "Change Password" : label}
                </Link>
              ))}
              {auth.authenticated ? (
                <button type="button" className="nav-link-button" onClick={logout}>
                  Logout
                </button>
              ) : null}
            </div>
          ) : null}
        </nav>
        <div className="sidebar-footer">
          <p>&copy; 2026 &ndash; Licensed Under AGPL&nbsp;V3</p>
        </div>
      </aside>
      <main className="content">
        <Routes>
            <Route path="/" element={<Navigate to={auth.authenticated ? "/read" : "/login"} replace />} />
          <Route path="/login" element={<LoginPage auth={auth} onAuthChanged={refreshAuth} />} />
            <Route path="/read" element={protect(<ReadPage />)} />
          <Route path="/status" element={protect(<StatusPage />)} />
          <Route path="/config" element={protect(<ConfigPage />)} />
          <Route path="/tuning" element={protect(<TuningPage />)} />
          <Route path="/labels" element={protect(<LabelsPage />)} />
          <Route path="/decisions" element={protect(<DecisionsPage />)} />
          <Route path="/logs" element={protect(<LogsPage />)} />
        </Routes>
      </main>
    </div>
  );
}
