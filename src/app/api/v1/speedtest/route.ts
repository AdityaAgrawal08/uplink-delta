import crypto from "crypto";

export async function GET() {
  const buf = new Uint8Array(262144); // 256 KB
  crypto.getRandomValues(buf);
  return new Response(buf, {
    headers: {
      "Content-Type": "application/octet-stream",
      "Content-Length": "262144",
      "Cache-Control": "no-store, no-cache, must-revalidate",
    },
  });
}
