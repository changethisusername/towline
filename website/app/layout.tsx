import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Towline — Agentic Engineering SDK for Portainer",
  description:
    "One command gives your AI agent a complete environment to build, deploy, and operate software on Portainer-managed infrastructure.",
  openGraph: {
    title: "Towline — Agentic Engineering SDK for Portainer",
    description:
      "One command gives your AI agent a complete environment to build, deploy, and operate software.",
    url: "https://towline.dev",
    siteName: "Towline",
    type: "website",
  },
  twitter: {
    card: "summary_large_image",
    title: "Towline",
    description: "Agentic Engineering SDK for Portainer",
  },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en" className="dark">
      <head>
        <link rel="preconnect" href="https://fonts.googleapis.com" />
        <link
          rel="preconnect"
          href="https://fonts.gstatic.com"
          crossOrigin="anonymous"
        />
        <link
          href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap"
          rel="stylesheet"
        />
      </head>
      <body className="min-h-screen bg-bg">{children}</body>
    </html>
  );
}
