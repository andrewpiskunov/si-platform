import { publicAPI } from '#lib/server/api.ts';
import type { Card } from '#lib/types.ts';
import type { PageServerLoad } from './$types';
export const load: PageServerLoad = async ({ params }) => {
  try {
    return {
      card: await publicAPI<Card>(`/catalogue/${encodeURIComponent(params.id)}`),
      error: ''
    };
  } catch (e) {
    return { card: null, error: e instanceof Error ? e.message : 'unavailable' };
  }
};
