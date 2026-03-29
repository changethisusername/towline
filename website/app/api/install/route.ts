import { NextResponse } from "next/server";

// The install script content — kept in sync with the repo root install.sh.
// When the repo is public, this fetches from GitHub raw. Until then, it's inlined.
const GITHUB_RAW_URL =
  "https://raw.githubusercontent.com/changethisusername/towline/main/install.sh";

export async function GET() {
  try {
    const res = await fetch(GITHUB_RAW_URL, {
      next: { revalidate: 300 }, // cache 5 min
    });

    if (res.ok) {
      const script = await res.text();
      return new NextResponse(script, {
        headers: {
          "Content-Type": "text/plain; charset=utf-8",
          "Cache-Control": "public, max-age=300, s-maxage=300",
        },
      });
    }
  } catch {
    // fallback below
  }

  // Fallback: redirect to GitHub releases page
  return NextResponse.redirect(
    "https://github.com/changethisusername/towline/releases",
    302
  );
}
