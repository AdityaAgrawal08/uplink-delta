import crypto from "crypto";
import path from "path";
import { argon2id } from "hash-wasm";

export async function hashPassword(password: string): Promise<string> {
  const salt = crypto.randomBytes(16);
  return argon2id({
    password,
    salt,
    iterations: 3,
    memorySize: 16384, // 16 MB
    parallelism: 1,
    hashLength: 32,
    outputType: "encoded",
  });
}

export async function verifyPassword(password: string, hash: string): Promise<boolean> {
  try {
    const parts = hash.split("$");
    // Expected format: $argon2id$v=19$m=16384,t=3,p=1$salt$hash
    // split gives: ["", "argon2id", "v=19", "m=16384,t=3,p=1", "salt", "hash"]
    if (parts.length < 6 || parts[1] !== "argon2id") {
      return false;
    }

    const paramParts = parts[3].split(",");
    let memorySize = 16384;
    let iterations = 3;
    let parallelism = 1;

    for (const param of paramParts) {
      const [key, value] = param.split("=");
      if (key === "m") memorySize = parseInt(value, 10);
      if (key === "t") iterations = parseInt(value, 10);
      if (key === "p") parallelism = parseInt(value, 10);
    }

    const saltBase64 = parts[4];
    // Re-add base64 padding if stripped
    const paddedSalt = saltBase64.padEnd(
      saltBase64.length + ((4 - (saltBase64.length % 4)) % 4),
      "="
    );
    const saltBytes = Buffer.from(paddedSalt, "base64");

    const recomputed = await argon2id({
      password,
      salt: saltBytes,
      iterations,
      memorySize,
      parallelism,
      hashLength: 32,
      outputType: "encoded",
    });

    return recomputed === hash;
  } catch (error) {
    console.error("Argon2id password verification failed:", error);
    return false;
  }
}

let ipAnonymizationSecret: string;
const envSecret = process.env.IP_ANONYMIZATION_SECRET;
if (!envSecret) {
  if (process.env.NODE_ENV === "production") {
    throw new Error("IP_ANONYMIZATION_SECRET environment variable is required in production");
  }
  // Development: generate ephemeral random secret
  ipAnonymizationSecret = crypto.randomBytes(32).toString("hex");
  console.warn("WARNING: IP_ANONYMIZATION_SECRET not set. Using ephemeral random secret (IPs will not be consistently anonymized across restarts).");
} else {
  ipAnonymizationSecret = envSecret;
}

export function anonymizeIp(ip: string): string {
  return crypto.createHmac("sha256", ipAnonymizationSecret).update(ip).digest("hex");
}

export function generateShareId(): string {
  // Generate 16 secure random bytes (128 bits of entropy)
  // Encode as URL-safe base64 string (22 characters)
  return crypto
    .randomBytes(16)
    .toString("base64")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

export function sanitizeFilename(filename: string): string {
  if (!filename) return "unnamed_file";
  // Extract basename to prevent path traversal
  const base = path.basename(filename);
  // Restrict to whitelisted characters [a-zA-Z0-9._-]
  let sanitized = base.replace(/[^a-zA-Z0-9._-]/g, "_");
  // Fallback if empty or purely special characters
  if (!sanitized || sanitized === "." || sanitized === "..") {
    sanitized = "file";
  }
  return sanitized;
}
