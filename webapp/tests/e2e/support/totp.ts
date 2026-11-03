import { createHmac } from "node:crypto";

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
const period = 30;

function decodeBase32(value: string): Buffer {
  let bits = 0;
  let accumulator = 0;
  const bytes: number[] = [];
  for (const character of value.replace(/=+$/, "").toUpperCase()) {
    const index = alphabet.indexOf(character);
    if (index < 0) throw new Error("invalid base32 authenticator secret");
    accumulator = (accumulator << 5) | index;
    bits += 5;
    if (bits >= 8) {
      bits -= 8;
      bytes.push((accumulator >>> bits) & 0xff);
    }
  }
  return Buffer.from(bytes);
}

export function currentTimeStep(now = Date.now()): number {
  return Math.floor(now / 1000 / period);
}

// totp computes the RFC 6238 SHA-1 six-digit code the server verifies.
export function totp(secret: string, timeStep: number): string {
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(timeStep));
  const digest = createHmac("sha1", decodeBase32(secret)).update(counter).digest();
  const offset = digest[digest.length - 1]! & 0x0f;
  const binary = digest.readUInt32BE(offset) & 0x7fffffff;
  return String(binary % 1_000_000).padStart(6, "0");
}

// nextTimeStep chooses a code the server will accept after `lastUsed` without
// waiting for the clock: the server accepts one step of skew either way and
// rejects any step at or before the last one consumed.
export function nextTimeStep(lastUsed: number): number {
  return Math.max(lastUsed + 1, currentTimeStep());
}
