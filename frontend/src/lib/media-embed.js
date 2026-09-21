// Recognising media that a record links to but does not contain.
//
// rekam stores records, not blobs. An image or clip written into a note as a
// URL is fetched by the reader's browser, from whoever hosts it, at the moment
// they open the record — so it can be slow, it can be gone, and it can be
// visible to the author and not to a teammate. That is a real property of the
// feature and the UI says so when a fetch fails, rather than showing a broken
// image icon and letting it read as rekam's bug.
//
// (Uploaded attachments are a different thing and stay on rekam's own origin —
// see spec/files.md. isSameOrigin below is what keeps the two apart.)

const IMAGE_EXT = /\.(png|jpe?g|gif|webp|avif|svg)$/i;
const VIDEO_EXT = /\.(mp4|webm|ogv|mov)$/i;

/** The kind of media a URL's path points at, if any. */
export function mediaKind(raw) {
  let u;
  try {
    u = new URL(String(raw).trim());
  } catch {
    return null;
  }
  // http(s) only. A data: or blob: URL in a record is either a mistake or an
  // attempt at something, and neither should become an element.
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return null;
  if (IMAGE_EXT.test(u.pathname)) return 'image';
  if (VIDEO_EXT.test(u.pathname)) return 'video';
  return null;
}

/** Whether a URL is served by rekam itself (an upload, not a link out). */
export function isSameOrigin(raw) {
  try {
    return new URL(String(raw), window.location.href).origin === window.location.origin;
  } catch {
    return false;
  }
}

/**
 * The host to name in the UI, for a link out.
 *
 * Worth showing before the reader clicks: an embed is a request their browser
 * makes to a third party, and which third party is the part they can judge.
 */
export function mediaHost(raw) {
  try {
    return new URL(String(raw), window.location.href).hostname.replace(/^www\./, '');
  } catch {
    return '';
  }
}
