import { get } from 'svelte/store';
import { apiMemories, apiCatalog } from './api.js';
import { memories, memoryTotal, memoryOffset, loading, taxonomyFilter, searchMode, catalog } from './store.js';
import { PAGE_SIZE } from './utils.js';

export { PAGE_SIZE };

export async function loadMemories(append = false) {
  if (get(searchMode)) return;

  const offset = append ? get(memoryOffset) + PAGE_SIZE : 0;
  const taxonomy = get(taxonomyFilter);

  loading.set(true);
  try {
    const data = await apiMemories({ taxonomy, limit: PAGE_SIZE, offset });
    const mems = data.memories || [];
    if (append) {
      memories.update(ms => [...ms, ...mems]);
      memoryOffset.set(offset);
    } else {
      memories.set(mems);
      memoryOffset.set(0);
    }
    memoryTotal.set(data.total ?? 0);
  } catch (_) {
    if (!append) { memories.set([]); memoryTotal.set(0); }
  } finally {
    loading.set(false);
  }
}

export async function loadCatalog() {
  try {
    const data = await apiCatalog();
    catalog.set(data.taxonomy || []);
  } catch (_) {}
}
