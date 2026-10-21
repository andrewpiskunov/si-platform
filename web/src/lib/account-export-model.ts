export type ExportKind = 'all' | 'pack' | 'theme' | 'upload';
export type ExportSubject = { id: string; name: string; deleted: boolean };
export type ExportSubjects = { items: ExportSubject[]; next_cursor: string | null };

export function exportURL(kind: ExportKind, id: string, metadataOnly: boolean): string | null {
  const query = new URLSearchParams();
  if (kind !== 'all') {
    if (!/^[a-f0-9]{32}$/.test(id)) return null;
    query.set('kind', kind);
    query.set('id', id);
  }
  if (metadataOnly) query.set('metadata_only', 'true');
  return `/api/privacy/export${query.size ? `?${query}` : ''}`;
}
