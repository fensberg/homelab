// The watchman: says when a site has gone quiet.
//
// Everything a site has to say reaches Slack from inside the site, so a site
// that has stopped - its cluster, its Prometheus, its Alertmanager, or the
// way out of the building - says nothing, and nothing looks exactly like
// nothing being wrong. The answer used to be a heartbeat posted to the
// channel twice a day, which left noticing its absence to a person.
//
// So each site's Alertmanager sends its always-firing alert here instead,
// and this, which runs outside every site, notices the absence itself. It
// writes down when it last heard from each site, looks at those times on a
// timer of its own, and says once that a site has gone quiet and once that
// it is heard from again.
//
// It can do one thing, which is post to the channel. What it is given comes
// from management/estate/watchman.tf:
//
//   HEARD          the store it keeps the times in
//   SITES          each site's key and the SHA-256 of the secret that site
//                  rings with - never the secret, so reading this Worker's
//                  settings yields nothing a site could be impersonated with
//   QUIET_AFTER_MINUTES   how long a silence is before it is said
//   SLACK_WEBHOOK  where it says it
//
// Written to run on the free plan and stay there: one write for each ring,
// and none on a look round unless a site has changed between quiet and
// heard.

const heardKey = site => `heard:${site}`;
const quietKey = site => `quiet:${site}`;

async function sha256Hex(text) {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(text));
  return [...new Uint8Array(digest)].map(b => b.toString(16).padStart(2, '0')).join('');
}

// Compared in full whatever differs, so how long it takes says nothing about
// where the first difference is.
function same(a, b) {
  let differs = a.length ^ b.length;
  for (let i = 0; i < a.length; i++) {
    differs |= a.charCodeAt(i) ^ b.charCodeAt(i % (b.length || 1));
  }
  return differs === 0;
}

// A site rings: POST /<site>, with the site's secret as a bearer token.
//
// Anything else is turned away the same way whatever was wrong with it, so
// an answer never says whether a site of that key exists.
export async function ring(request, env, now) {
  if (request.method !== 'POST') {
    return new Response(null, {status: 405});
  }
  const site = new URL(request.url).pathname.slice(1);
  const expected = JSON.parse(env.SITES)[site];
  const presented = (request.headers.get('Authorization') || '').replace(/^Bearer /, '');
  const offered = await sha256Hex(presented);
  if (typeof expected !== 'string' || !same(offered, expected)) {
    return new Response(null, {status: 401});
  }
  await env.HEARD.put(heardKey(site), String(now));
  return new Response(null, {status: 204});
}

async function say(env, text, post) {
  const answer = await post(env.SLACK_WEBHOOK, {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({text}),
  });
  if (!answer.ok) {
    // Thrown before anything is written down, so the next look round finds
    // the same change and says it again.
    throw new Error(`the channel answered ${answer.status}`);
  }
}

// A look round: every site's last ring against the clock.
//
// A site that has never rung is not quiet. It has been granted its secret
// and not yet built, and saying so every ten minutes would teach the
// channel to ignore this.
export async function lookRound(env, now, post = fetch) {
  const quietAfter = Number(env.QUIET_AFTER_MINUTES) * 60 * 1000;
  if (!(quietAfter > 0)) {
    throw new Error('QUIET_AFTER_MINUTES is not a length of time, so no silence would ever be long enough to say');
  }
  for (const site of Object.keys(JSON.parse(env.SITES)).sort()) {
    const last = await env.HEARD.get(heardKey(site));
    if (last === null) {
      continue;
    }
    const quiet = now - Number(last) > quietAfter;
    const said = await env.HEARD.get(quietKey(site));
    if (quiet && said === null) {
      await say(env,
        `:rotating_light: *${site} has gone quiet.* Nothing has been heard from its alerting since ` +
        `${new Date(Number(last)).toISOString()}. The cluster, its Prometheus, its Alertmanager or the way ` +
        'out of the site has stopped, and no other alert from that site will reach this channel until it is ' +
        'heard from again.', post);
      await env.HEARD.put(quietKey(site), last);
    } else if (!quiet && said !== null) {
      const minutes = Math.round((Number(last) - Number(said)) / 60000);
      await say(env, `:white_check_mark: *${site} is heard from again*, ${minutes} minutes after it was last heard.`, post);
      await env.HEARD.delete(quietKey(site));
    }
  }
}

export default {
  fetch: (request, env) => ring(request, env, Date.now()),
  scheduled: (event, env) => lookRound(env, Date.now()),
};
