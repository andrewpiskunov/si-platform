import { publicAPI } from '#lib/server/api.ts';
import type { Card } from '#lib/types.ts';
import type { PageServerLoad } from './$types';
export const load: PageServerLoad = async ({ url }) => {
  const query = new URLSearchParams();
  for (const key of ['q', 'language', 'tag', 'difficulty', 'media', 'age', 'max_bytes', 'offset']) {
    const value = url.searchParams.get(key);
    if (value) query.set(key, value);
  }
  try {
    const result = await publicAPI<{ items: Card[] | null }>(`/catalogue?${query}`);
    return { items: result.items || [], failed: false, filters: Object.fromEntries(query) };
  } catch {
    return { items: [] as Card[], failed: true, filters: Object.fromEntries(query) };
  }
};
