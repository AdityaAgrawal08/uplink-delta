import {
  S3Client,
  PutObjectCommand,
  GetObjectCommand,
  DeleteObjectCommand,
  HeadObjectCommand,
} from "@aws-sdk/client-s3";
import { getSignedUrl } from "@aws-sdk/s3-request-presigner";

const accessKeyId = process.env.R2_ACCESS_KEY_ID;
const secretAccessKey = process.env.R2_SECRET_ACCESS_KEY;
const endpoint = process.env.R2_ENDPOINT_URL;
const bucketName = process.env.R2_BUCKET_NAME;

export let s3Client: S3Client | null = null;

const isPlaceholder = (val: string | undefined) => {
  if (!val) return true;
  return (
    val.includes("<") ||
    val.includes(">") ||
    val.includes("_here") ||
    val.includes("example")
  );
};

if (
  accessKeyId &&
  secretAccessKey &&
  endpoint &&
  !isPlaceholder(accessKeyId) &&
  !isPlaceholder(secretAccessKey) &&
  !isPlaceholder(endpoint)
) {
  try {
    s3Client = new S3Client({
      region: "auto",
      endpoint: endpoint,
      credentials: {
        accessKeyId: accessKeyId,
        secretAccessKey: secretAccessKey,
      },
    });
  } catch (err) {
    console.error("Failed to initialize S3 client, falling back to mock mode:", err);
    s3Client = null;
  }
} else {
  console.log("Cloudflare R2 credentials missing or contain placeholders. Running in local dev file mock mode.");
}

export function getBucketName() {
  return bucketName || "uplink-bucket";
}

export async function getPresignedUploadUrl(
  objectKey: string,
  expiresInSeconds: number,
  mimeType: string,
  hashValue: string // SHA-256
): Promise<string> {
  if (!s3Client) {
    // Local dev mock route
    return `${process.env.APP_URL || "http://localhost:3000"}/api/v1/mock-r2-upload?key=${encodeURIComponent(objectKey)}&sha256=${encodeURIComponent(hashValue)}&mimeType=${encodeURIComponent(mimeType)}`;
  }

  // Convert hex hash to base64 for S3 ChecksumSHA256 header validation
  const base64Hash = Buffer.from(hashValue, "hex").toString("base64");

  const command = new PutObjectCommand({
    Bucket: getBucketName(),
    Key: objectKey,
    ContentType: mimeType,
    ChecksumSHA256: base64Hash,
  });

  return getSignedUrl(s3Client, command, { expiresIn: expiresInSeconds });
}

export async function getPresignedDownloadUrl(
  objectKey: string,
  expiresInSeconds: number,
  storageFilename: string,
  mimeType: string,
  preview: boolean
): Promise<string> {
  if (!s3Client) {
    // Local dev mock route
    return `${process.env.APP_URL || "http://localhost:3000"}/api/v1/mock-r2-download?key=${encodeURIComponent(objectKey)}&preview=${preview}&filename=${encodeURIComponent(storageFilename)}&mimeType=${encodeURIComponent(mimeType)}`;
  }

  const contentDisposition = preview
    ? "inline"
    : `attachment; filename="${storageFilename}"`;
  
  const contentType = preview ? mimeType : "application/octet-stream";

  const command = new GetObjectCommand({
    Bucket: getBucketName(),
    Key: objectKey,
    ResponseContentDisposition: contentDisposition,
    ResponseContentType: contentType,
  });

  return getSignedUrl(s3Client, command, { expiresIn: expiresInSeconds });
}

export interface ObjectDetails {
  size: number;
  checksumSha256?: string;
  contentType?: string;
  exists: boolean;
  error?: string;
}

export async function checkObjectExists(objectKey: string): Promise<ObjectDetails> {
  if (!s3Client) {
    // Mock local dev file check
    const fs = require("fs");
    const path = require("path");
    const localPath = path.join(process.cwd(), "uploads_dev", objectKey);
    if (fs.existsSync(localPath)) {
      const stats = fs.statSync(localPath);
      // Retrieve stored mock checksum if available
      let checksum = "";
      try {
        const metaPath = localPath + ".meta";
        if (fs.existsSync(metaPath)) {
          const meta = JSON.parse(fs.readFileSync(metaPath, "utf-8"));
          checksum = meta.sha256;
        }
      } catch {}
      return {
        size: stats.size,
        checksumSha256: checksum || "mock_sha256_hash",
        exists: true,
      };
    }
    return { size: 0, exists: false, error: "Mock file not found locally" };
  }

  try {
    const command = new HeadObjectCommand({
      Bucket: getBucketName(),
      Key: objectKey,
    });
    const response = await s3Client.send(command);
    return {
      size: response.ContentLength || 0,
      checksumSha256: response.ChecksumSHA256,
      contentType: response.ContentType,
      exists: true,
    };
  } catch (error: any) {
    if (error.name === "NotFound" || error.$metadata?.httpStatusCode === 404) {
      return { size: 0, exists: false, error: "Object not found" };
    }
    return { size: 0, exists: false, error: error?.message || "Error checking object" };
  }
}

export async function deleteObject(objectKey: string): Promise<boolean> {
  if (!s3Client) {
    const fs = require("fs");
    const path = require("path");
    const localPath = path.join(process.cwd(), "uploads_dev", objectKey);
    try {
      if (fs.existsSync(localPath)) {
        fs.unlinkSync(localPath);
      }
      const metaPath = localPath + ".meta";
      if (fs.existsSync(metaPath)) {
        fs.unlinkSync(metaPath);
      }
      return true;
    } catch (err) {
      console.error("Local mock delete failed:", err);
      return false;
    }
  }

  try {
    const command = new DeleteObjectCommand({
      Bucket: getBucketName(),
      Key: objectKey,
    });
    await s3Client.send(command);
    return true;
  } catch (error) {
    console.error(`Failed to delete object ${objectKey} from R2:`, error);
    return false;
  }
}
