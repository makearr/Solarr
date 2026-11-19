import { describe, expect, it } from "vitest";

import { fromBase64, sealSecrets, sealWith } from "./seal";

// The vector internal/api/auth/transport_test.go opens: the browser and the server agree byte for byte.
const vectorPublicKey = "16eh9YxjRWIIP4Hj/YvbagfDZOl7AhAMVRbtMuIdcFY=";
const vectorSealed =
  "encv1:+0Gx/PdcEif6CIdgVqXtB6xE/gwRbsLegLjlI5WdeCQ=:AAECAwQFBgcICQoL:RnnyFiO0hEc0zyOfISH9F/sfrgpweUYSyHiNdfA=";

describe("sealWith", () => {
  it("seals exactly as the server expects", () => {
    const ephemeral = new TextEncoder().encode("e".repeat(32));
    const nonce = Uint8Array.from({ length: 12 }, (_, i) => i);
    expect(sealWith(fromBase64(vectorPublicKey), "a secret pass", ephemeral, nonce)).toBe(vectorSealed);
  });

  it("uses a fresh key and nonce for every value", () => {
    const key = fromBase64(vectorPublicKey);
    expect(sealWith(key, "same")).not.toBe(sealWith(key, "same"));
  });
});

describe("sealSecrets", () => {
  it("seals passwords, API keys and credentials at any depth, and nothing else", () => {
    const sealed = sealSecrets(
      {
        username: "avs",
        password: "pw",
        options: { category: "music", credentials: "user:pw" },
        keys: [{ tmdb_api_key: "k1" }],
        api_key: "",
        sort_key: "title",
      },
      (plain) => `sealed(${plain})`,
    );
    expect(sealed).toEqual({
      username: "avs",
      password: "sealed(pw)",
      options: { category: "music", credentials: "sealed(user:pw)" },
      keys: [{ tmdb_api_key: "sealed(k1)" }],
      api_key: "",
      sort_key: "title",
    });
  });
});

describe("a proxy URL that names a user", () => {
  it("is sealed, and one without a user is left as typed", () => {
    const sealed = sealSecrets(
      { proxy_url: "socks5://kim:pw@proxy:1080", egress_proxy_url: "http://proxy:3128", other: { proxy_url: "http://u:p@h:1" } },
      (plain) => `sealed(${plain})`,
    );
    expect(sealed).toEqual({
      proxy_url: "sealed(socks5://kim:pw@proxy:1080)",
      egress_proxy_url: "http://proxy:3128",
      other: { proxy_url: "sealed(http://u:p@h:1)" },
    });
  });
});
