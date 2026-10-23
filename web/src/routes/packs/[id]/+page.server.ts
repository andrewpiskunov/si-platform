import { publicAPI } from '#lib/server/api.ts';
import type { Card, SourceDetail } from '#lib/types.ts';
import type { PageServerLoad } from './$types';
export const load: PageServerLoad = async ({ params }) => {
  try {
    return {
      card: await publicAPI<Card>(`/catalogue/${encodeURIComponent(params.id)}`),
      detail: await publicAPI<SourceDetail>(`/catalogue/${encodeURIComponent(params.id)}/metadata`).catch(() => null),
      error: ''
    };
  } catch (e) {
    return { card: null, detail: null, error: e instanceof Error ? e.message : 'unavailable' };
  }
};
