// ProseMirror plugin: upload images pasted or dropped into the Milkdown editor.
//
// On paste/drop of an image file it uploads via `upload(file)` (→ { url }),
// then inserts a commonmark image node pointing at the returned capability URL.
// The markdown serializes natively as ![alt](url); nothing custom is stored.

import { Plugin, PluginKey } from '@milkdown/prose/state';

// Insert an image node with the given src at `pos` (or the current selection).
function insertImage(view, url, alt, pos) {
  const { state } = view;
  const imageType = state.schema.nodes.image;
  if (!imageType) return;
  const node = imageType.create({ src: url, alt: alt || '' });
  const at = pos == null ? state.selection.to : pos;
  view.dispatch(state.tr.insert(at, node));
}

// Upload one file and drop it in at `pos`. Errors are surfaced via onError.
async function uploadAndInsert(view, file, pos, upload, onError) {
  try {
    const { url } = await upload(file);
    // The view may have changed while awaiting; clamp the insert position.
    const size = view.state.doc.content.size;
    insertImage(view, url, file.name?.replace(/\.[^.]+$/, ''), Math.min(pos ?? size, size));
  } catch (err) {
    onError?.(err);
  }
}

function imageFiles(list) {
  return Array.from(list || []).filter((f) => f.type.startsWith('image/'));
}

// upload: (File) => Promise<{ url }>. onError: (err) => void (optional).
export function imageUpload(upload, onError) {
  return new Plugin({
    key: new PluginKey('rekam-image-upload'),
    props: {
      handlePaste(view, event) {
        const files = imageFiles(event.clipboardData?.files);
        if (files.length === 0) return false;
        event.preventDefault();
        for (const file of files) uploadAndInsert(view, file, null, upload, onError);
        return true;
      },
      handleDrop(view, event) {
        const files = imageFiles(event.dataTransfer?.files);
        if (files.length === 0) return false;
        event.preventDefault();
        const coords = view.posAtCoords({ left: event.clientX, top: event.clientY });
        const pos = coords ? coords.pos : null;
        for (const file of files) uploadAndInsert(view, file, pos, upload, onError);
        return true;
      },
    },
  });
}

// Programmatic insert used by the toolbar's file-picker button.
export function insertImageFromFile(view, file, upload, onError) {
  return uploadAndInsert(view, file, null, upload, onError);
}
