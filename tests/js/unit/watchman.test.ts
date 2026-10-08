import { describe, expect, it } from "vitest";
import { createHash } from "node:crypto";
import { ring, lookRound } from "../../../management/estate/watchman/watchman.mjs";

// The watchman says when a site has gone quiet, once, and once that it is
// heard from again. The clock, the store and the channel are stand-ins.
const minute = 60 * 1000;
const secret = "what-north-rings-with";
const sha = (s: string) => createHash("sha256").update(s).digest("hex");

function estate() {
  const kept = new Map<string, string>();
  const writes: string[] = [];
  const said: string[] = [];
  const env = {
    SITES: JSON.stringify({ north: sha(secret), south: sha("what-south-rings-with") }),
    QUIET_AFTER_MINUTES: "25",
    SLACK_WEBHOOK: "https://channel.invalid/hook",
    HEARD: {
      get: async (k: string) => kept.get(k) ?? null,
      put: async (k: string, v: string) => {
        writes.push(k);
        kept.set(k, v);
      },
      delete: async (k: string) => {
        writes.push(k);
        kept.delete(k);
      },
    },
  };
  let channelUp = true;
  // Stands in for fetch, and answers with only what the watchman reads.
  const post = (async (url: string, init: { body: string }) => {
    if (!channelUp) return { ok: false, status: 503 };
    expect(url).toBe(env.SLACK_WEBHOOK);
    said.push(JSON.parse(init.body).text);
    return { ok: true, status: 200 };
  }) as unknown as typeof fetch;
  return { env, kept, writes, said, post, channel: (up: boolean) => (channelUp = up) };
}

const rings = (site: string, bearer: string | null, method = "POST") =>
  new Request(`https://watchman.invalid/${site}`, {
    method,
    headers: bearer === null ? {} : { Authorization: `Bearer ${bearer}` },
  });

describe("a site ringing", () => {
  it("is written down when it rings with its own secret", async () => {
    const e = estate();
    expect((await ring(rings("north", secret), e.env, 1000)).status).toBe(204);
    expect(e.kept.get("heard:north")).toBe("1000");
  });

  it("is turned away with another site's secret, a wrong one, or none", async () => {
    const e = estate();
    for (const bearer of ["what-south-rings-with", "wrong", "", null]) {
      expect((await ring(rings("north", bearer), e.env, 1000)).status).toBe(401);
    }
    expect(e.kept.size).toBe(0);
  });

  it("is turned away the same way for a site nobody was granted", async () => {
    const e = estate();
    expect((await ring(rings("west", secret), e.env, 1000)).status).toBe(401);
    expect((await ring(rings("", secret), e.env, 1000)).status).toBe(401);
    expect(e.kept.size).toBe(0);
  });

  it("is turned away when it is not a ring", async () => {
    const e = estate();
    expect((await ring(rings("north", secret, "GET"), e.env, 1000)).status).toBe(405);
    expect(e.kept.size).toBe(0);
  });
});

describe("a look round", () => {
  it("says nothing about a site that rang lately, and writes nothing", async () => {
    const e = estate();
    await ring(rings("north", secret), e.env, 0);
    e.writes.length = 0;
    await lookRound(e.env, 25 * minute, e.post);
    expect(e.said).toHaveLength(0);
    expect(e.writes).toHaveLength(0);
  });

  it("says nothing about a site that has never rung", async () => {
    const e = estate();
    await lookRound(e.env, 1000 * minute, e.post);
    expect(e.said).toHaveLength(0);
  });

  it("says once that a site has gone quiet, naming the site and when it was last heard", async () => {
    const e = estate();
    await ring(rings("north", secret), e.env, 0);
    await lookRound(e.env, 26 * minute, e.post);
    await lookRound(e.env, 36 * minute, e.post);
    await lookRound(e.env, 46 * minute, e.post);
    expect(e.said).toHaveLength(1);
    expect(e.said[0]).toContain("north has gone quiet");
    expect(e.said[0]).toContain("1970-01-01T00:00:00.000Z");
    expect(e.said[0]).not.toContain("south");
  });

  it("says once that a quiet site is heard from again", async () => {
    const e = estate();
    await ring(rings("north", secret), e.env, 0);
    await lookRound(e.env, 26 * minute, e.post);
    await ring(rings("north", secret), e.env, 90 * minute);
    await lookRound(e.env, 96 * minute, e.post);
    await lookRound(e.env, 106 * minute, e.post);
    expect(e.said).toHaveLength(2);
    expect(e.said[1]).toContain("north is heard from again");
    expect(e.said[1]).toContain("90 minutes");
  });

  it("says it on the next look round when the channel did not take it", async () => {
    const e = estate();
    await ring(rings("north", secret), e.env, 0);
    e.channel(false);
    await expect(lookRound(e.env, 26 * minute, e.post)).rejects.toThrow("503");
    e.channel(true);
    await lookRound(e.env, 36 * minute, e.post);
    expect(e.said).toHaveLength(1);
  });

  it("refuses to look when it was given no length of silence", async () => {
    const e = estate();
    e.env.QUIET_AFTER_MINUTES = "";
    await expect(lookRound(e.env, 0, e.post)).rejects.toThrow("QUIET_AFTER_MINUTES");
  });
});
