import { publicAPI } from '#lib/server/api.ts';
import { roomListQuery } from '#lib/room-preferences.ts';
import type { PublicRooms } from '#lib/types.ts';
import type { PageServerLoad } from './$types';

export const load: PageServerLoad = async ({ url, setHeaders }) => {
  setHeaders({ 'cache-control': 'no-store' });
  let query: string;
  try {
    query = roomListQuery(url.searchParams);
  } catch {
    return { items: [], next: '', after: '', limit: 50, error: 'invalid' };
  }
  const params = new URLSearchParams(query);
  const page = { after: params.get('after') || '', limit: Number(params.get('limit')) };
  try {
    const result = await publicAPI<PublicRooms>(`/rooms?${query}`);
    return { ...page, items: result.items || [], next: result.next_after || '', error: '' };
  } catch {
    return { ...page, items: [], next: '', error: 'unavailable' };
  }
};
