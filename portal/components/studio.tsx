"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { ArrowLeft, ArrowRight, Check, CheckCheck, CircleHelp, Clock3, Film, LockKeyhole, MessageSquare, Play, RotateCcw, Send, X } from "lucide-react";
import { type Action, type Note, type Review, type Status, type Workspace, demoWorkspace } from "@/lib/types";

const labels: Record<Status, string> = { pending: "For review", approved: "Approved", changes: "Changes needed" };
const dateLabel = (value: string, full = false) => new Intl.DateTimeFormat("en-GB", { day: "numeric", month: full ? "long" : "short", timeZone: "Europe/London" }).format(new Date(value + "T12:00:00Z"));
export const formatTimestamp = (seconds: number) => { const total = Math.max(0, Math.floor(seconds)); return `${Math.floor(total / 60).toString().padStart(2, "0")}:${(total % 60).toString().padStart(2, "0")}`; };
const timeLabel = (value: string) => new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", timeZone: "Europe/London" }).format(new Date(value));

export function Studio({ localPreview }: { localPreview: boolean }) {
  const [workspace, setWorkspace] = useState<Workspace | null>(null);
  const [activeId, setActiveId] = useState("");
  const [tab, setTab] = useState<Status>("pending");
  const [error, setError] = useState("");
  const [loadError, setLoadError] = useState("");
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const [mode, setMode] = useState<Action>("note");
  const [currentTime, setCurrentTime] = useState(0);
  const [noteTime, setNoteTime] = useState(0);
  const [attachTime, setAttachTime] = useState(true);
  const videoRef = useRef<HTMLVideoElement>(null);
  const [help, setHelp] = useState(false);
  const [notice, setNotice] = useState("");
  const detail = useRef<HTMLElement>(null);
  const notesEnd = useRef<HTMLDivElement>(null);

  async function load() {
    setLoadError("");
    if (localPreview) {
      const demo = demoWorkspace(); setWorkspace(demo); setActiveId(demo.reviews[0].id); return;
    }
    try {
      const response = await fetch("/api/portal", { cache: "no-store", credentials: "same-origin" });
      if (!response.ok) throw new Error(response.status === 401 ? "Open the private review link we sent you to access your studio." : "We couldn't load your videos. Please try again in a moment.");
      const data: Workspace = await response.json(); setWorkspace(data);
      setActiveId((current) => data.reviews.some((r) => r.id === current) ? current : (data.reviews.find((r) => r.status === "pending") ?? data.reviews[0])?.id ?? "");
    } catch (cause) { setLoadError(cause instanceof Error ? cause.message : "We couldn't load your studio."); }
  }

  useEffect(() => { void load(); /* The local preview is fixed for this build. */ }, [localPreview]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { setNote(""); setError(""); setMode("note"); setCurrentTime(0); setNoteTime(0); setAttachTime(true); }, [activeId, tab]);
  useEffect(() => { if (!notice) return; const timer = setTimeout(() => setNotice(""), 6500); return () => clearTimeout(timer); }, [notice]);
  useEffect(() => {
    if (localPreview || note.trim() || mode !== "note" || busy) return;
    const controller = new AbortController();
    const timer = setInterval(async () => {
      if (document.visibilityState !== "visible" || (videoRef.current && !videoRef.current.paused)) return;
      try {
        const response = await fetch("/api/portal", { cache: "no-store", credentials: "same-origin", signal: controller.signal });
        if (response.status === 401) { setLoadError("Open your private review link again to access your videos."); return; }
        if (!response.ok) return;
        const data: Workspace = await response.json();
        setWorkspace(data);
        setActiveId((current) => data.reviews.some((review) => review.id === current) ? current : (data.reviews.find((review) => review.status === "pending") ?? data.reviews[0])?.id ?? "");
      } catch { /* Keep the current review and retry next minute. */ }
    }, 60000);
    return () => { controller.abort(); clearInterval(timer); };
  }, [localPreview, note, mode, busy]);

  const reviews = useMemo(() => workspace?.reviews.filter((r) => r.status === tab).sort((a, b) => a.postingDate.localeCompare(b.postingDate)) ?? [], [workspace, tab]);
  const active = reviews.find((r) => r.id === activeId) ?? reviews[0];
  const month = active ? new Intl.DateTimeFormat("en-GB", { month: "long", year: "numeric" }).format(new Date(active.postingDate + "T12:00:00Z")) : "Your content studio";

  function select(review: Review) { setActiveId(review.id); if (window.innerWidth < 800) detail.current?.scrollIntoView({ behavior: "smooth", block: "start" }); }

  function captureTime() {
    const video = videoRef.current;
    if (video) { video.pause(); setNoteTime(video.currentTime); setCurrentTime(video.currentTime); }
    else setNoteTime(currentTime);
  }

  function seekTo(seconds: number) {
    const video = videoRef.current;
    if (!video) return;
    video.pause();
    const target = Math.min(seconds, Number.isFinite(video.duration) ? video.duration : seconds);
    video.currentTime = Math.max(0, target);
    setCurrentTime(target); setNoteTime(target);
  }

  function chooseMode(next: Action) { captureTime(); setMode(next); setError(""); }

  async function submit(action: Action, text: string) {
    if (!workspace || !active || busy) return;
    if (action === "changes" && !text.trim()) { setError("Let us know what you'd like us to change."); return; }
    if (action === "note" && !text.trim()) return;
    setBusy(true); setError("");
    try {
      if (localPreview) {
        const saved: Note = { id: crypto.randomUUID(), text: text.trim(), author: "Client reviewer", createdAt: new Date().toISOString(), kind: action, timestampSeconds: attachTime && action !== "approved" ? Math.round(noteTime * 1000) / 1000 : undefined };
        setWorkspace((current) => current && ({ ...current, reviews: current.reviews.map((review) => review.id !== active.id ? review : { ...review, status: action === "note" ? review.status : action, decidedAt: action === "note" ? review.decidedAt : saved.createdAt, notes: [...review.notes, saved] }) }));
      } else {
        const response = await fetch("/api/portal/action", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json", "X-CSRF-Token": workspace.csrf }, body: JSON.stringify({ reviewId: active.id, action, note: text.trim(), version: active.version, requestId: crypto.randomUUID(), timestampSeconds: attachTime && action !== "approved" ? Math.round(noteTime * 1000) / 1000 : undefined }) });
        const data = await response.json();
        if (!response.ok) throw new Error(data.error || "Your feedback wasn't saved. Please try again.");
        setWorkspace(data);
      }
      setNote(""); setMode("note");
      if (action !== "note") { setTab(action); setNotice(action === "approved" ? "Video approved." : "Change request saved."); }
      else { setNotice("Your note has been added."); setTimeout(() => notesEnd.current?.scrollIntoView({ behavior: "smooth", block: "nearest" }), 100); }
    } catch (cause) { setError(cause instanceof Error ? cause.message : "We couldn't save your feedback."); }
    finally { setBusy(false); }
  }

  return <>
    <a className="skip-link" href="#workspace">Skip to your videos</a>
    {workspace?.demo && <div className="demo-banner"><span>Local preview</span> Sample videos. Feedback here is for testing only.</div>}
    <header className="site-header"><a className="brand" href="/portal/" aria-label="RestoRefine content studio"><img src="/portal/restorefine-logoblack.svg" alt="RestoRefine" width="151" height="32" /><span className="brand-divider" /><span className="brand-product">Video reviews</span></a><div className="header-right"><button className="help-button" onClick={() => setHelp(true)}><CircleHelp size={17} /> <span>How it works</span></button></div></header>

    <main id="workspace" className="studio-shell">
      <section className="studio-intro" aria-label="Your workspace"><div><h1>{workspace?.client.name ?? "Video reviews"}</h1><p>Watch your videos, approve them or tell us what to change.</p></div><p className="intro-month">{month}</p></section>

      <div className="workspace-nav"><div className="tabs" role="tablist" aria-label="Video status">{(["pending", "approved", "changes"] as Status[]).map((status) => <button id={`tab-${status}`} key={status} role="tab" aria-selected={tab === status} aria-controls="review-panel" onClick={() => { setTab(status); setError(""); }} className={tab === status ? "tab active" : "tab"}>{labels[status]} <span>({workspace?.reviews.filter((r) => r.status === status).length ?? 0})</span></button>)}</div></div>

      {loadError ? <div className="empty-state" role="alert"><LockKeyhole size={28} /><h2>Unable to open your videos</h2><p>{loadError}</p><button className="button primary" onClick={() => void load()}>Try again <RotateCcw size={16} /></button></div>
      : !workspace ? <div className="workspace-loading" aria-label="Loading your videos" aria-busy="true"><div className="skeleton stage-skeleton" /><div className="skeleton notes-skeleton" /></div>
      : !active ? <div className="empty-state"><CheckCheck size={34} /><h2>{tab === "pending" ? "No videos waiting for review." : tab === "approved" ? "No approved videos yet." : "No change requests."}</h2><p>{tab === "pending" ? "Your next previews will appear here when they're ready." : tab === "approved" ? "Videos you approve will be kept here." : "Videos needing another pass will appear here."}</p>{tab !== "pending" && <button className="button secondary" onClick={() => setTab("pending")}>Back to review <ArrowRight size={16} /></button>}</div>
      : <section id="review-panel" role="tabpanel" aria-labelledby={`tab-${tab}`} className="review-workspace" ref={detail}>
        <div className="video-column"><div className="video-stage reel-stage">
          {active.previewUrl ? <video
            ref={videoRef}
            key={active.id}
            src={active.previewUrl}
            controls
            playsInline
            preload="metadata"
            poster={active.poster}
            aria-label={`Video preview: ${active.title}`}
            onTimeUpdate={(event) => setCurrentTime(event.currentTarget.currentTime)}
            onSeeked={(event) => { const at = event.currentTarget.currentTime; setCurrentTime(at); if (!note.trim()) setNoteTime(at); }}
            onPause={(event) => { if (!note.trim()) setNoteTime(event.currentTarget.currentTime); }}
            onError={() => setError("This preview couldn't load. Please try again in a moment.")}
          /> : <div className="decision-stamp">{active.status === "approved" ? <Check size={26} /> : active.status === "changes" ? <RotateCcw size={26} /> : <Clock3 size={26} />}<span>{active.status === "pending" ? "Preview is processing" : labels[active.status]}</span></div>}
        </div>
          <div className="video-heading"><div><h2>{active.title}</h2><div className="video-meta"><span>Posting {dateLabel(active.postingDate, true)}</span><span>Version {active.version}</span></div></div></div>
          <div className="video-footer"><span>{active.status === "pending" ? "" : `Review completed ${active.decidedAt ? timeLabel(active.decidedAt) + " UK" : ""}.`}</span><span>{reviews.findIndex((r) => r.id === active.id) + 1} of {reviews.length}<button aria-label="Previous video" disabled={reviews.length < 2} onClick={() => select(reviews[(reviews.findIndex((r) => r.id === active.id) - 1 + reviews.length) % reviews.length])}><ArrowLeft size={17} /></button><button aria-label="Next video" disabled={reviews.length < 2} onClick={() => select(reviews[(reviews.findIndex((r) => r.id === active.id) + 1) % reviews.length])}><ArrowRight size={17} /></button></span></div>
        </div>

        <aside className="feedback-panel" aria-label="Feedback and approval">
          <div className="feedback-heading"><h3>Feedback</h3><span className="feedback-count">{active.notes.length} {active.notes.length === 1 ? "note" : "notes"}</span></div>
          <div className="notes-list" aria-live="polite">
            {active.notes.length === 0 ? <div className="notes-empty"><p>No feedback yet.</p><span>Pause the video where you want to leave a note.</span></div> : active.notes.map((item) => <article className={`note-item ${item.kind}`} key={item.id}>
              <div className="note-author"><span className={item.author === "RestoRefine" ? "note-avatar team" : "note-avatar"}>{item.author === "RestoRefine" ? <img src="/portal/restorefine-logowhite.svg" alt="" width={23} height={24} /> : workspace.client.initials}</span><div><strong>{item.author}</strong><time dateTime={item.createdAt}>{timeLabel(item.createdAt)} UK</time></div></div>
              {item.timestampSeconds !== undefined && <button className="note-timestamp" onClick={() => seekTo(item.timestampSeconds!)} disabled={!active.previewUrl} aria-label={`Seek to ${formatTimestamp(item.timestampSeconds)}`}><Play size={13} fill="currentColor" />{formatTimestamp(item.timestampSeconds)}</button>}
              {item.text && <p>{item.text}</p>}
              {item.kind !== "note" && <span className="note-kind">{labels[item.kind]}</span>}
            </article>)}
            <div ref={notesEnd} />
          </div>
          {active.status === "pending" ? <>
            <form className="note-form inline-composer" onSubmit={(event) => { event.preventDefault(); void submit(mode, note); }}>
              <div className="composer-heading"><label htmlFor="review-note">{mode === "changes" ? "What needs changing?" : mode === "approved" ? "Approve this version?" : "Add a note"}</label>{mode !== "note" && <button type="button" className="cancel-mode" onClick={() => setMode("note")} disabled={busy}>Cancel</button>}</div>
              {mode === "approved" && <p className="approval-explanation">This version will be marked as approved for posting.</p>}
              <textarea id="review-note" maxLength={4000} value={note} onChange={(event) => setNote(event.target.value)} onFocus={captureTime} placeholder={mode === "approved" ? "Add a final note (optional)" : "Describe your feedback…"} rows={3} disabled={busy} required={mode !== "approved"} />
              {mode !== "approved" && <div className="timestamp-option"><label><input type="checkbox" checked={attachTime} onChange={(event) => { setAttachTime(event.target.checked); if (event.target.checked) captureTime(); }} /><span>Attach playback time</span></label><button type="button" onClick={captureTime} title="Update to the current playback position"><Clock3 size={14} />{formatTimestamp(noteTime)}</button></div>}
              <button className="button primary send-feedback" type="submit" disabled={busy || (mode !== "approved" && !note.trim())}>{busy ? "Saving…" : mode === "changes" ? "Send change request" : mode === "approved" ? "Confirm approval" : "Send note"}{mode === "note" ? <Send size={16} /> : <ArrowRight size={17} />}</button>
              {mode === "note" && <p className="composer-hint">A note keeps the video in review.</p>}
            </form>
            {mode === "note" && <div className="review-actions"><button className="button secondary" onClick={() => chooseMode("changes")} disabled={busy}><RotateCcw size={16} /> Request changes</button><button className="button approve-outline" onClick={() => chooseMode("approved")} disabled={busy}><Check size={17} /> Approve video</button></div>}
          </> : <div className="review-complete"><CheckCheck size={22} /><strong>{active.status === "approved" ? "Video approved" : "Changes requested"}</strong><p>{active.status === "approved" ? "Your approval has been saved." : "You will be notified when a revised version is ready."}</p></div>}
          {error && <p className="inline-error" role="alert">{error}</p>}
        </aside>
      </section>}

      {reviews.length > 1 && <section className="queue-section" aria-label="More videos"><div className="queue-heading"><h3>More videos</h3><span>{reviews.length} videos · {labels[tab].toLowerCase()}</span></div><div className="video-queue">{reviews.map((review) => <button className={`queue-card ${active?.id === review.id ? "selected" : ""}`} key={review.id} onClick={() => select(review)} aria-pressed={active?.id === review.id}><span className="queue-image">{review.poster ? <img src={review.poster} alt="" loading="lazy" /> : <Film size={28} />}<span className="queue-play">{review.status === "approved" ? <Check size={18} /> : <Play size={15} fill="currentColor" />}</span></span><strong>{review.title}</strong><span className="queue-meta">{dateLabel(review.postingDate)}<span>Version {review.version}</span></span></button>)}</div></section>}
      <footer className="studio-footer"><span>RestoRefine client reviews</span><img src="/portal/restorefine-logoblack.svg" alt="RestoRefine" width="112" height="24" /><span><LockKeyhole size={12} /> Keep this link within your team.</span></footer>
    </main>

    <Dialog.Root open={help} onOpenChange={setHelp}><Dialog.Portal><Dialog.Overlay className="modal-overlay" /><Dialog.Content className="modal-content help-modal"><Dialog.Close className="modal-close" aria-label="Close"><X size={20} /></Dialog.Close><Dialog.Title>Reviewing your videos</Dialog.Title><Dialog.Description>Watch each video, then approve it or request changes.</Dialog.Description><ol><li><Play size={19} /><div><strong>Watch the video</strong><p>Choose a video and watch the preview.</p></div></li><li><MessageSquare size={19} /><div><strong>Leave feedback</strong><p>Pause or scrub to a moment, then add a note with its playback time.</p></div></li><li><Check size={19} /><div><strong>Approve</strong><p>Approve the version you want posted.</p></div></li></ol><p className="help-private"><LockKeyhole size={15} /> This private link gives access to your workspace. Share it only with your review team.</p></Dialog.Content></Dialog.Portal></Dialog.Root>
    {notice && <div className="notice" role="status"><Check size={17} />{notice}<button onClick={() => setNotice("")} aria-label="Dismiss"><X size={15} /></button></div>}
  </>;
}
