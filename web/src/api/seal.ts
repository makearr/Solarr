import { x25519 } from "@noble/curves/ed25519";
import { hkdf } from "@noble/hashes/hkdf";
import { sha256 } from "@noble/hashes/sha2";
import { gcm } from "@noble/ciphers/aes";

/** A secret sealed for the server (internal/api/auth/transport.go): X25519 with a fresh key per value, HKDF-SHA256,
 *  AES-256-GCM. Pure JavaScript, because WebCrypto is missing on plain-HTTP pages, the very case this is for. */
export const SEALED_PREFIX = "encv1:";
const SEAL_INFO = "solarr:transport-seal:v1";

/** sealWith encrypts one value for the server's public key; the ephemeral key and nonce are only fixed by tests. */
export function sealWith(
  serverPublic: Uint8Array,
  plain: string,
  ephemeral: Uint8Array = x25519.utils.randomSecretKey(),
  nonce: Uint8Array = crypto.getRandomValues(new Uint8Array(12)),
): string {
  const shared = x25519.getSharedSecret(ephemeral, serverPublic);
  const key = hkdf(sha256, shared, undefined, SEAL_INFO, 32);
  const ciphertext = gcm(key, nonce).encrypt(new TextEncoder().encode(plain));
  return `${SEALED_PREFIX}${base64(x25519.getPublicKey(ephemeral))}:${base64(nonce)}:${base64(ciphertext)}`;
}

/** isSecretField names the request fields that carry a password, an API key or client credentials. */
export function isSecretField(name: string): boolean {
  return name === "password" || name === "api_key" || name === "credentials" || name.endsWith("_api_key");
}

/** sealSecrets returns the value with every non-empty secret field sealed, at any depth. */
export function sealSecrets(value: unknown, seal: (plain: string) => string): unknown {
  if (Array.isArray(value)) return value.map((item) => sealSecrets(item, seal));
  if (typeof value !== "object" || value === null) return value;
  return Object.fromEntries(
    Object.entries(value).map(([name, item]) => [
      name,
      isSecretField(name) && typeof item === "string" && item !== "" && !item.startsWith(SEALED_PREFIX)
        ? seal(item)
        : sealSecrets(item, seal),
    ]),
  );
}

export function base64(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

export function fromBase64(text: string): Uint8Array {
  return Uint8Array.from(atob(text), (char) => char.charCodeAt(0));
}
