export interface SaveRequest<T> {
  base_revision: number;
  document: T;
  generation: number;
}
const copy = <T>(value: T): T => JSON.parse(JSON.stringify(value));
export class DraftEditor<T> {
  document: T;
  revision: number;
  dirty = false;
  saving = false;
  conflict = false;
  private generation = 0;
  private history: T[] = [];
  constructor(document: T, revision: number) {
    this.document = copy(document);
    this.revision = revision;
  }
  edit(document: T) {
    this.history.push(copy(this.document));
    if (this.history.length > 100) this.history.shift();
    this.document = copy(document);
    this.generation++;
    this.dirty = true;
  }
  undo() {
    const previous = this.history.pop();
    if (previous) {
      this.document = previous;
      this.generation++;
      this.dirty = true;
    }
  }
  get canUndo() {
    return this.history.length > 0;
  }
  beginSave(): SaveRequest<T> | null {
    if (!this.dirty || this.saving || this.conflict) return null;
    this.saving = true;
    return {
      base_revision: this.revision,
      document: copy(this.document),
      generation: this.generation
    };
  }
  saved(request: SaveRequest<T>, document: T, revision: number) {
    this.revision = revision;
    this.saving = false;
    if (request.generation === this.generation) {
      this.document = copy(document);
      this.dirty = false;
    }
  }
  failed(code: string) {
    this.saving = false;
    this.conflict = code === 'conflict';
  }
  rebase(revision: number) {
    this.revision = revision;
    this.conflict = false;
  }
}
