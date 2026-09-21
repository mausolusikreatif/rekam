// Shared YouTube/Vimeo URL parsing for video embeds.
//
// A bare YouTube/Vimeo URL on its own line in a note becomes a sandboxed iframe
// in the preview. Only these two providers are recognised, and the video id is
// validated against a strict charset before it's ever placed in an iframe src —
// so no attacker-controlled string reaches the embed URL.

const YT_ID = /^[A-Za-z0-9_-]{6,20}$/;
const VIMEO_ID = /^[0-9]{6,12}$/;

// parseVideoUrl returns { provider, id, embedUrl } for a recognised YouTube or
// Vimeo URL, or null. embedUrl is safe to use as an iframe src.
export function parseVideoUrl(raw) {
  let u;
  try {
    u = new URL(String(raw).trim());
  } catch {
    return null;
  }
  const host = u.hostname.replace(/^www\./, '').toLowerCase();

  // YouTube: youtu.be/<id>, youtube.com/watch?v=<id>, /embed/<id>, /shorts/<id>
  if (host === 'youtu.be') {
    const id = u.pathname.slice(1);
    if (YT_ID.test(id)) {
      return { provider: 'youtube', id, embedUrl: `https://www.youtube-nocookie.com/embed/${id}` };
    }
  }
  if (host === 'youtube.com' || host === 'm.youtube.com' || host === 'youtube-nocookie.com') {
    let id = '';
    if (u.pathname === '/watch') id = u.searchParams.get('v') || '';
    else {
      const m = /^\/(embed|shorts|v)\/([^/?#]+)/.exec(u.pathname);
      if (m) id = m[2];
    }
    if (YT_ID.test(id)) {
      return { provider: 'youtube', id, embedUrl: `https://www.youtube-nocookie.com/embed/${id}` };
    }
  }

  // Vimeo: vimeo.com/<id>, player.vimeo.com/video/<id>
  if (host === 'vimeo.com' || host === 'player.vimeo.com') {
    const m = /(\d{6,12})/.exec(u.pathname);
    const id = m ? m[1] : '';
    if (VIMEO_ID.test(id)) {
      return { provider: 'vimeo', id, embedUrl: `https://player.vimeo.com/video/${id}` };
    }
  }

  return null;
}

export const isVideoUrl = (raw) => parseVideoUrl(raw) !== null;
