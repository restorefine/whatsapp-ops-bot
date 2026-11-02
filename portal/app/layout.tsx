import type { Metadata } from "next";
import localFont from "next/font/local";
import "./globals.css";

const holiday = localFont({ src: "../public/fonts/Holiday.otf", variable: "--font-holiday", display: "swap" });

export const metadata: Metadata = {
  title: "Your content studio | RestoRefine",
  description: "Your private space to watch, review and approve your RestoRefine videos.",
  robots: { index: false, follow: false },
};

export default function Layout({ children }: { children: React.ReactNode }) {
  return <html lang="en-GB" className={holiday.variable}><body>{children}</body></html>;
}
