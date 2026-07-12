import { NextRequest } from "next/server";

export function validateAdminAuth(req: NextRequest): boolean {
  const expected = process.env.ADMIN_API_KEY;
  if (!expected) {
    console.error("ADMIN_API_KEY environment variable is not set.");
    return false;
  }
  const authHeader = req.headers.get("authorization");
  return authHeader === `Bearer ${expected}`;
}
