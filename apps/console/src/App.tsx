import type {
  AdministratorCredentialsRequest,
  AdministratorLoginResponse,
} from "@ariad/contracts";
import { useQueryClient } from "@tanstack/react-query";
import { Link, Outlet } from "@tanstack/react-router";
import { type FormEvent, useEffect, useState } from "react";

import { adminTokenStorageKey, administratorIDStorageKey, adminUnauthorizedEvent, requestJSON, sendJSON, surface } from "./api";

export function ConsoleShell() {
  const queryClient = useQueryClient();
  const [authenticated, setAuthenticated] = useState(
    () => sessionStorage.getItem(adminTokenStorageKey) !== null,
  );

  useEffect(() => {
    const handleUnauthorized = () => {
      queryClient.clear();
      setAuthenticated(false);
    };
    window.addEventListener(adminUnauthorizedEvent, handleUnauthorized);
    return () => window.removeEventListener(adminUnauthorizedEvent, handleUnauthorized);
  }, [queryClient]);

  if (!authenticated) {
    return <AdminLoginPage onLogin={() => setAuthenticated(true)} />;
  }

  async function logout() {
    try { await sendJSON<never, { logged_out: boolean }>("/api/v1/admin/logout", "POST"); } finally {
      sessionStorage.removeItem(adminTokenStorageKey);
      sessionStorage.removeItem(administratorIDStorageKey);
      queryClient.clear();
      setAuthenticated(false);
    }
  }

  return (
    <div className="console-shell">
      <aside className="sidebar">
        <a className="brand" href="/" aria-label="Ariad home">
          <span className="brand-mark">A</span>
          <span>Ariad</span>
        </a>
        <nav aria-label="Primary navigation">
          <span className="nav-label">Workspace</span>
          <Link className="nav-item" activeOptions={{ exact: true }} activeProps={{ className: "nav-item nav-item-active" }} to="/">Overview</Link>
          <span className="nav-item nav-item-disabled">Agents</span>
          <Link className="nav-item" activeProps={{ className: "nav-item nav-item-active" }} to="/knowledge">Knowledge</Link>
          <Link className="nav-item" activeProps={{ className: "nav-item nav-item-active" }} to="/playground">Playground</Link>
          <Link className="nav-item" activeProps={{ className: "nav-item nav-item-active" }} to="/conversations" search={{ visitorId: "" }}>Conversations</Link>
          <Link className="nav-item" activeProps={{ className: "nav-item nav-item-active" }} to="/visitors">Visitors</Link>
          <Link className="nav-item" activeProps={{ className: "nav-item nav-item-active" }} to="/reviews">Review queue</Link>
          <Link className="nav-item" activeProps={{ className: "nav-item nav-item-active" }} to="/settings/model">Model configuration</Link>
          <Link className="nav-item" activeProps={{ className: "nav-item nav-item-active" }} to="/settings/administrators">Administrators</Link>
          <span className="nav-item nav-item-disabled">Evaluations</span>
        </nav>
        <button className="logout-button" type="button" onClick={() => void logout()}>Log out</button>
      </aside>
      <main className="main-content" data-surface={surface}>
        <Outlet />
      </main>
    </div>
  );
}

function AdminLoginPage({ onLogin }: { onLogin: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmed = username.trim();
    if (!trimmed || !password) return;
    setError("");
    setPending(true);
    try {
      const login = await requestJSON<AdministratorLoginResponse>("/api/v1/admin/login", null, false, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username: trimmed, password } satisfies AdministratorCredentialsRequest) });
      sessionStorage.setItem(adminTokenStorageKey, login.token);
      sessionStorage.setItem(administratorIDStorageKey, login.administrator.administrator_id);
      onLogin();
    } catch (loginError) {
      setError(loginError instanceof Error ? loginError.message : "Could not sign in.");
    } finally {
      setPending(false);
    }
  }

  return (
    <main className="login-page" data-surface={surface}>
      <form className="login-card" onSubmit={(event) => void submit(event)}>
        <span className="brand-mark">A</span>
        <div className="eyebrow">Ariad Console</div>
        <h1>Administrator access</h1>
        <p>Sign in with your administrator account. On first startup, find the generated admin password in the API startup log.</p>
        <label htmlFor="admin-username">Username</label>
        <input
          id="admin-username"
          autoComplete="username"
          autoFocus
          value={username}
          onChange={(event) => setUsername(event.target.value)}
        />
        <label htmlFor="admin-password">Password</label>
        <input id="admin-password" type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} />
        {error && <p className="login-error" role="alert">{error}</p>}
        <button type="submit" disabled={pending || !username.trim() || !password}>
          {pending ? "Checking…" : "Continue"}
        </button>
      </form>
    </main>
  );
}

export function OverviewPage() {
  return (
    <section className="overview">
      <div className="eyebrow">Foundation / Console</div>
      <h1>Answers people can verify.</h1>
      <p className="lede">
        Connect knowledge, inspect every decision, and publish an agent that
        knows when not to answer.
      </p>
      <div className="status-card">
        <div><span className="status-dot" aria-hidden="true" /><strong>Console runtime ready</strong></div>
        <p>Router and server-state boundaries are initialized.</p>
      </div>
    </section>
  );
}
