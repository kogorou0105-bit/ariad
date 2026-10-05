import type { AppSurface } from "@ariad/contracts";

const surface: AppSurface = "widget";

export function WidgetPreview() {
  return (
    <main className="preview" data-surface={surface}>
      <section className="widget" aria-label="Ariad support preview">
        <header>
          <div className="avatar" aria-hidden="true">A</div>
          <div>
            <strong>Ariad Support</strong>
            <span><i /> Grounded in verified sources</span>
          </div>
        </header>
        <div className="conversation">
          <p className="label">Today</p>
          <div className="message">
            Hi — ask me about this product. I’ll cite the sources I use, and say
            when I don’t have enough evidence.
          </div>
        </div>
        <form onSubmit={(event) => event.preventDefault()}>
          <label htmlFor="question" className="sr-only">Ask a question</label>
          <input id="question" placeholder="Ask a question…" autoComplete="off" />
          <button type="submit" aria-label="Send question">→</button>
        </form>
      </section>
    </main>
  );
}
