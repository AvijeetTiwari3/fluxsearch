import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "FluxSearch — Distributed Full-Text & Semantic Engine",
  description: "High-performance distributed search engine featuring BM25 ranking, HNSW vector graph, and Reciprocal Rank Fusion built in Go.",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body className="min-h-screen bg-[#090a0f] text-[#f3f4f6] antialiased selection:bg-indigo-500/30 selection:text-indigo-200">
        {children}
      </body>
    </html>
  );
}
