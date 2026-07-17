import crypto from "crypto";

export class BloomFilter {
  bits: Buffer;
  m: number;
  k: number;

  constructor(m: number, k: number, bits?: Buffer) {
    this.m = m;
    this.k = k;
    this.bits = bits || Buffer.alloc(Math.ceil(m / 8));
  }

  /**
   * Generates k hash indices for a string using double hashing of SHA-256 hash.
   */
  private getIndices(item: string): number[] {
    const hash = crypto.createHash("sha256").update(item).digest();
    const indices: number[] = [];
    const hash1 = hash.readUInt32BE(0);
    const hash2 = hash.readUInt32BE(4);
    for (let i = 0; i < this.k; i++) {
      // Double hashing: (hash1 + i * hash2) % m
      const index = Math.abs((hash1 + i * hash2) % this.m);
      indices.push(index);
    }
    return indices;
  }

  add(item: string): void {
    const indices = this.getIndices(item);
    for (const idx of indices) {
      const byteIdx = Math.floor(idx / 8);
      const bitIdx = idx % 8;
      this.bits[byteIdx] |= (1 << bitIdx);
    }
  }

  contains(item: string): boolean {
    const indices = this.getIndices(item);
    for (const idx of indices) {
      const byteIdx = Math.floor(idx / 8);
      const bitIdx = idx % 8;
      if ((this.bits[byteIdx] & (1 << bitIdx)) === 0) {
        return false;
      }
    }
    return true;
  }

  toJSON() {
    return {
      m: this.m,
      k: this.k,
      bits: this.bits.toString("base64"),
    };
  }

  static fromJSON(json: { m: number; k: number; bits: any }): BloomFilter {
    let buf: Buffer;
    if (!json.bits) {
      buf = Buffer.alloc(Math.ceil(json.m / 8));
    } else if (typeof json.bits === "string") {
      buf = Buffer.from(json.bits, "base64");
    } else if (Buffer.isBuffer(json.bits)) {
      buf = json.bits;
    } else if (json.bits.buffer && Buffer.isBuffer(json.bits.buffer)) {
      buf = json.bits.buffer;
    } else if (typeof json.bits.value === "function") {
      // Handle mongodb.Binary object
      buf = json.bits.value();
    } else {
      buf = Buffer.from(json.bits);
    }
    return new BloomFilter(json.m, json.k, buf);
  }
}
