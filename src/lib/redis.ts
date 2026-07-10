import { Redis } from "@upstash/redis";

const redisUrl = process.env.UPSTASH_REDIS_REST_URL;
const redisToken = process.env.UPSTASH_REDIS_REST_TOKEN;

export interface IRedisClient {
  get(key: string): Promise<unknown>;
  set(key: string, value: unknown, options?: { ex?: number; px?: number; nx?: boolean }): Promise<unknown>;
  incr(key: string): Promise<number>;
  expire(key: string, seconds: number): Promise<number>;
}

class MockRedis implements IRedisClient {
  private store: Map<string, { value: unknown; expiry: number | null }> = new Map();

  async get(key: string): Promise<unknown> {
    const item = this.store.get(key);
    if (!item) return null;
    if (item.expiry && Date.now() > item.expiry) {
      this.store.delete(key);
      return null;
    }
    // Return parsed JSON if it looks like serialized object
    if (typeof item.value === "string") {
      try {
        return JSON.parse(item.value);
      } catch {
        return item.value;
      }
    }
    return item.value;
  }

  async set(key: string, value: unknown, options?: { ex?: number; px?: number; nx?: boolean }): Promise<unknown> {
    if (options?.nx) {
      const existing = await this.get(key);
      if (existing !== null) {
        return null;
      }
    }

    let expiry: number | null = null;
    if (options?.ex) {
      expiry = Date.now() + options.ex * 1000;
    } else if (options?.px) {
      expiry = Date.now() + options.px;
    }

    const valueToStore = typeof value === "object" && value !== null ? JSON.stringify(value) : value;
    this.store.set(key, { value: valueToStore, expiry });
    return "OK";
  }

  async incr(key: string): Promise<number> {
    const item = this.store.get(key);
    let val = 0;
    if (item) {
      if (item.expiry && Date.now() > item.expiry) {
        this.store.delete(key);
      } else {
        val = parseInt(item.value as string, 10);
        if (isNaN(val)) val = 0;
      }
    }
    val += 1;
    this.store.set(key, { value: String(val), expiry: item?.expiry || null });
    return val;
  }

  async expire(key: string, seconds: number): Promise<number> {
    const item = this.store.get(key);
    if (!item) return 0;
    item.expiry = Date.now() + seconds * 1000;
    return 1;
  }
}

let redis: IRedisClient;

if (redisUrl && redisToken) {
  redis = new Redis({
    url: redisUrl,
    token: redisToken,
  }) as unknown as IRedisClient;
} else {
  if (process.env.NODE_ENV === "production") {
    console.error("Warning: Redis credentials missing in production. Falling back to in-memory MockRedis.");
  } else {
    console.log("Upstash Redis credentials missing. Using local in-memory MockRedis.");
  }
  redis = new MockRedis();
}

export { redis };
