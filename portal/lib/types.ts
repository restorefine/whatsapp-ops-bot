export type Status = "pending" | "approved" | "changes";
export type Note = { id: string; text: string; author: string; createdAt: string; kind: "note" | "approved" | "changes"; timestampSeconds?: number };
export type Review = {
  id: string; title: string; postingDate: string; version: number; status: Status;
  duration?: string; format?: string; poster?: string; previewUrl?: string;
  createdAt: string; decidedAt?: string; notes: Note[];
};
export type Workspace = { client: { id: string; name: string; initials: string }; reviews: Review[]; csrf: string; demo: boolean };
export type Action = "note" | "approved" | "changes";

export function demoWorkspace(): Workspace {
  const today = new Date();
  const date = (offset: number) => { const value = new Date(today); value.setDate(value.getDate() + offset); return value.toISOString().slice(0, 10); };
  const earlier = new Date(today.getTime() - 90 * 60000).toISOString();
  return {
    client: { id: "demo", name: "Blanc Events", initials: "BE" }, csrf: "demo", demo: true,
    reviews: [
      { id: "demo-1", title: "Venue tour", postingDate: date(3), version: 2, status: "pending", duration: "0:12", format: "Instagram Reel", poster: "/portal/industries/hospitality.webp", previewUrl: "/portal/demo-preview.mp4", createdAt: earlier, notes: [{ id: "n1", text: "Opening shot updated. Logo timing adjusted.", author: "RestoRefine", createdAt: earlier, kind: "note" }] },
      { id: "demo-2", title: "Event setup", postingDate: date(6), version: 1, status: "pending", duration: "0:12", format: "Instagram Reel", poster: "/portal/services/content.webp", previewUrl: "/portal/demo-preview.mp4", createdAt: earlier, notes: [] },
      { id: "demo-3", title: "November bookings", postingDate: date(9), version: 1, status: "pending", duration: "0:12", format: "Social video", poster: "/portal/restorefine-hero.webp", previewUrl: "/portal/demo-preview.mp4", createdAt: earlier, notes: [] },
      { id: "demo-4", title: "Table settings", postingDate: date(-2), version: 1, status: "approved", poster: "/portal/industries/hospitality.webp", createdAt: earlier, decidedAt: earlier, notes: [{ id: "n2", text: "Looks lovely, thank you!", author: "Client reviewer", createdAt: earlier, kind: "approved" }] },
      { id: "demo-5", title: "Entrance and reception", postingDate: date(12), version: 1, status: "changes", poster: "/portal/services/content.webp", createdAt: earlier, decidedAt: earlier, notes: [{ id: "n3", text: "Use the newer entrance shot.", timestampSeconds: 8, author: "Client reviewer", createdAt: earlier, kind: "changes" }] },
    ],
  };
}
