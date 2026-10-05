import { Outlet } from "@tanstack/react-router";

import type { AppSurface } from "@ariad/contracts";

const surface: AppSurface = "console";

export function ConsoleShell() {
  return (
    <div className="console-shell">
      <aside className="sidebar">
        <a className="brand" href="/" aria-label="Ariad home">
          <span className="brand-mark">A</span>
          <span>Ariad</span>
        </a>
        <nav aria-label="Primary navigation">
          <span className="nav-label">Workspace</span>
          <a className="nav-item nav-item-active" href="/">Overview</a>
          <span className="nav-item nav-item-disabled">Agents</span>
          <span className="nav-item nav-item-disabled">Knowledge</span>
          <span className="nav-item nav-item-disabled">Conversations</span>
          <span className="nav-item nav-item-disabled">Evaluations</span>
        </nav>
      </aside>
      <main className="main-content" data-surface={surface}>
        <Outlet />
      </main>
    </div>
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
