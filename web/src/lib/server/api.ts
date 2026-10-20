import { API_ORIGIN } from '$app/env/private';
export async function publicAPI<T>(path: string): Promise<T> {
  const response = await fetch(new URL(`/api${path}`, API_ORIGIN), {
    signal: AbortSignal.timeout(8000)
  });
  if (!response.ok) throw new Error(response.status === 404 ? 'not_found' : 'unavailable');
  return response.json();
}
