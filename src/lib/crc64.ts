const POLY = BigInt("0x9A6C9329AC4BC9B5");

const table = new BigUint64Array(256);
for (let i = 0; i < 256; i++) {
  let crc = BigInt(i);
  for (let j = 0; j < 8; j++) {
    if (crc & BigInt(1)) {
      crc = (crc >> BigInt(1)) ^ POLY;
    } else {
      crc >>= BigInt(1);
    }
  }
  table[i] = crc;
}

export function crc64nvme(data: Uint8Array, previousCrc = BigInt(0)): bigint {
  let crc = previousCrc;
  for (let i = 0; i < data.length; i++) {
    const lookupIndex = Number((crc ^ BigInt(data[i])) & BigInt(0xff));
    crc = table[lookupIndex] ^ (crc >> BigInt(8));
  }
  return crc;
}

export function crc64nvmeBase64(data: Uint8Array): string {
  const crc = crc64nvme(data);
  const buf = new Uint8Array(8);
  const view = new DataView(buf.buffer);
  view.setBigUint64(0, crc, false); // false = big-endian
  return Buffer.from(buf).toString("base64");
}
