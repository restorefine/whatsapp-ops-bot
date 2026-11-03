"use client";

import { useCallback, useEffect, useState } from "react";
import { ArrowDownToLine, ArrowUpRight, RefreshCw, ChevronLeft, ChevronRight } from "lucide-react";
import styles from "./editor-counts.module.css";

type Record = { taskId: string; title: string; list: string; url: string; completedAt: string; recordedAt: string; status: string; batch: number; position: number; imported: boolean; issue?: string };
type Editor = { key: string; name: string; number?: string; total: number; historicalTotal: number; fullBatches: number; currentBatch: number; currentCount: number; remaining: number; reviewCount: number; records: Record[]; warnings: string[]; startedAt: string; inactive: boolean };
type Alert = { id: string; editorKey: string; batch: number; to: string; state: string; createdAt: string };
type Report = { startedAt: string; checkedAt: string; batchSize: number; editors: Editor[]; alerts: Alert[] };

const formatDate = (value: string) => new Intl.DateTimeFormat("en-GB", { timeZone: "Europe/London", day: "numeric", month: "short", year: "numeric", hour: "2-digit", minute: "2-digit" }).format(new Date(value));

export function EditorCounts() {
  const [report, setReport] = useState<Report | null>(null);
  const [selected, setSelected] = useState("");
  const [batch, setBatch] = useState("current");
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(async (signal?: AbortSignal) => {
    setBusy(true);
    try {
      const response = await fetch("/api/admin/editors", { cache: "no-store", credentials: "same-origin", signal });
      if (response.status === 401) throw new Error("Your admin session expired. Send /editors to the bot for a fresh private link.");
      const data = await response.json();
      if (!response.ok) throw new Error(data.error || "The complete count could not be verified. Please try again.");
      setReport(data); setError("");
      setSelected(current => current || data.editors[0]?.key || "");
    } catch (cause) {
      if (signal?.aborted) return;
      setError(cause instanceof Error ? cause.message : "Could not load the report.");
    } finally { if (!signal?.aborted) setBusy(false); }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    const timer = setInterval(() => { if (document.visibilityState === "visible") void load(controller.signal); }, 60_000);
    return () => { controller.abort(); clearInterval(timer); };
  }, [load]);

  const editor = report?.editors.find(item => item.key === selected);
  const batchNumber = batch === "current" ? editor?.currentBatch : Number(batch);
  const rows = (editor?.records || []).filter(row => batch === "history" ? row.imported : !row.imported && row.batch === batchNumber).filter(row => `${row.title} ${row.taskId} ${row.list}`.toLowerCase().includes(search.toLowerCase())).slice().reverse();
  const pageCount = Math.max(1, Math.ceil(rows.length / 30));
  const activePage = Math.min(page, pageCount - 1);
  const displayRows = rows.slice(activePage * 30, (activePage + 1) * 30);
  const alerts = (report?.alerts || []).filter(item => item.editorKey === selected && item.batch === batchNumber);
  const changeView = () => { setPage(0); setSearch(""); };

  return <div className={styles.shell}>
    <a className="skip-link" href="#editor-report">Skip to report</a>
    <header className={styles.header}>
      <div className={styles.brand}><img src="/portal/restorefine-logoblack.svg" alt="RestoRefine" width="42" height="42" /><span>RestoRefine</span></div>
      <span className={styles.access}>Admin report</span>
    </header>
    <main id="editor-report">
      <div className={styles.titleRow}>
        <div><h1>Video editors</h1><p>20 completed videos per cycle. Every cycle stays here for audit.</p></div>
        <div className={styles.actions}>
          <button type="button" onClick={() => void load()} disabled={busy} aria-label="Refresh editor counts"><RefreshCw size={18} aria-hidden="true" />{busy ? "Checking…" : "Refresh"}</button>
          {report && !error && <a href="/api/admin/editors.xlsx"><ArrowDownToLine size={18} aria-hidden="true" />Download Excel</a>}
        </div>
      </div>
      {error && <div className={styles.error} role="alert">{error}{report && <p>The table below is the last successful check, not a current verified count.</p>}</div>}
      {!report && !error && <p role="status" className={styles.loading}>Checking completed tasks in ClickUp…</p>}
      {report && <>
        <p className={styles.checked}>Checked {formatDate(report.checkedAt)} UK · Updates every minute while this page is open</p>
        <nav className={styles.editors} aria-label="Select video editor">{report.editors.map(item => <button type="button" key={item.key} aria-current={selected === item.key ? "page" : undefined} onClick={() => { setSelected(item.key); setBatch("current"); changeView(); }}><span>{item.name}{item.inactive ? " (inactive)" : ""}</span><span>{item.currentCount}/20</span></button>)}</nav>
        {editor && <>
          <section className={styles.summary} aria-label={`${editor.name} cycle summary`}>
            <div className={styles.progress}><h2>{editor.name}</h2>{editor.number && <p>+{editor.number}</p>}<div><strong>{editor.currentCount}<span> / 20</span></strong><span>Cycle {editor.currentBatch}</span></div><progress max="20" value={editor.currentCount} aria-label={`${editor.currentCount} of 20 videos in cycle ${editor.currentBatch}`} /><p>{editor.remaining} more {editor.remaining === 1 ? "video" : "videos"} to complete this cycle.</p></div>
            <dl className={styles.totals}><div><dt>Completed cycles</dt><dd>{editor.fullBatches}</dd></div><div><dt>Videos in new cycles</dt><dd>{editor.total}</dd></div><div><dt>Earlier historical videos</dt><dd>{editor.historicalTotal}</dd></div><div><dt>Evidence issues to review</dt><dd>{editor.reviewCount}</dd></div></dl>
          </section>
          <div className={styles.rules}><p>New cycles started {formatDate(editor.startedAt)} UK. Tasks already completed at the first scan are kept in historical evidence and do not count towards these cycles.</p><p>A full cycle with verified evidence is ready for payment review. Check any flagged tasks before arranging payment.</p></div>
          <section aria-labelledby="evidence-title" className={styles.evidence}>
            <div className={styles.tableHeader}><h2 id="evidence-title">Video evidence</h2><div className={styles.filters}><label>View<select value={batch} onChange={event => { setBatch(event.target.value); changeView(); }}><option value="current">Current cycle {editor.currentBatch}</option>{Array.from({ length: editor.fullBatches }, (_, i) => editor.fullBatches - i).map(value => <option key={value} value={value}>Cycle {value} · 20 videos</option>)}<option value="history">Earlier historical videos ({editor.historicalTotal})</option></select></label><label className={styles.search}>Find a task<input type="search" value={search} onChange={event => { setSearch(event.target.value); setPage(0); }} placeholder="Title or task ID" /></label></div></div>
            {batch !== "history" && alerts.length > 0 && <p className={styles.receipts}>Admin alerts: {alerts.filter(item => item.state === "sent").length} sent · {alerts.filter(item => item.state === "queued").length} waiting for delivery{alerts.some(item => item.state === "uncertain" || item.state === "sending") ? " · Some deliveries need checking; no automatic retry" : ""}.</p>}
            <div className={styles.tableScroll}><table><thead><tr><th scope="col">No.</th><th scope="col">Video / ClickUp proof</th><th scope="col">Completed (UK)</th><th scope="col">Audit record</th></tr></thead><tbody>{displayRows.map(row => <tr key={row.taskId}><td>{row.imported ? "—" : row.position}</td><td><a href={row.url} target="_blank" rel="noopener noreferrer">{row.title}<ArrowUpRight size={15} aria-hidden="true" /></a><small>{row.list} · {row.taskId}</small></td><td><time dateTime={row.completedAt}>{formatDate(row.completedAt)}</time><small>Original status: {row.status}</small></td><td>{row.issue ? <span className={styles.issue}>{row.issue}</span> : <span>{row.imported ? "Historical evidence" : "Recorded complete"}</span>}<small>Recorded {formatDate(row.recordedAt)}</small></td></tr>)}</tbody></table>{rows.length === 0 && <div className={styles.empty}>{search ? "No tasks match your search." : batch === "history" ? "No earlier completed tasks were found at the first scan." : "No videos recorded in this cycle yet. New completed tasks will appear here automatically."}</div>}</div>
            {pageCount > 1 && <div className={styles.pagination}><span>{rows.length} tasks · Page {activePage + 1} of {pageCount}</span><div><button type="button" disabled={activePage === 0} onClick={() => setPage(activePage - 1)} aria-label="Previous tasks"><ChevronLeft size={20} /></button><button type="button" disabled={activePage + 1 === pageCount} onClick={() => setPage(activePage + 1)} aria-label="Next tasks"><ChevronRight size={20} /></button></div></div>}
          </section>
          {editor.warnings.length > 0 && <details className={styles.warnings}><summary>Excluded tasks to review ({editor.warnings.length})</summary><ul>{editor.warnings.map((warning, index) => <li key={index}>{warning}</li>)}</ul></details>}
          <p className={styles.method}>Counting rule: one completed top-level task in the editor’s personal ClickUp list equals one video. Only Complete, Completed or Done statuses count. Held work, subtasks, shared editor assignments and tasks without a completion timestamp are excluded. Reopened or missing tasks stay in their original cycle and are flagged for audit. ClickUp proof links use your existing ClickUp access.</p>
        </>}
      </>}
    </main>
    <footer className={styles.footer}><span>RestoRefine operations</span><span>Private admin access · Send /editors on WhatsApp for a new link</span></footer>
  </div>;
}
