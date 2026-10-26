import { redirect } from '@sveltejs/kit';
import type { PageServerLoad } from './$types';

// Keep existing catalogue search links and room context working at the new URL.
export const load: PageServerLoad = ({ url }) => {
  const catalogueKeys = ['q', 'language', 'tag', 'difficulty', 'media', 'age', 'max_bytes', 'offset', 'room'];
  if (catalogueKeys.some((key) => url.searchParams.has(key))) {
    redirect(307, `/packs${url.search}`);
  }
  return {};
};
