export interface User {
  id: string;
  username: string;
  display_name: string;
  role: string;
  created_at: string;
}
export interface Rights {
  license: string;
  source_url: string;
  attribution: string;
  allow_download: boolean;
  confirmed: boolean;
}
export interface Card {
  id: string;
  name: string;
  owner_id: string;
  author: string;
  status: string;
  language: string;
  restriction: string;
  revision: number;
  difficulty: number;
  question_count: number;
  round_count: number;
  size_bytes: number;
  has_media: boolean;
  allow_download: boolean;
  tags: string[] | null;
  rights: Rights;
  updated_at: string;
}
export interface Info {
  authors?: string[];
  sources?: string[];
  comments?: string;
  showman_comments?: string;
  extension?: string;
}
export interface Content {
  type: string;
  value: string;
  is_ref?: boolean;
  placement?: string;
  duration_ms?: number;
  wait_for_finish?: boolean;
}
export interface Parameter {
  name: string;
  type?: string;
  value?: string;
  items?: Content[];
  params?: Parameter[];
  number_set?: { value?: string; minimum?: number; maximum?: number; step?: number };
}
export interface Question {
  id: string;
  price: number;
  type?: string;
  info: Info;
  params: Parameter[];
  right: string[];
  wrong?: string[];
  has_script?: boolean;
}
export interface Theme {
  id: string;
  name: string;
  info: Info;
  questions: Question[];
}
export interface Round {
  id: string;
  name: string;
  type?: string;
  info: Info;
  themes: Theme[];
}
export interface PackDocument {
  id: string;
  name: string;
  version: string;
  source_version?: string;
  date?: string;
  publisher?: string;
  language?: string;
  restriction?: string;
  difficulty?: number;
  logo?: string;
  contact_uri?: string;
  generator?: string;
  tags?: string[];
  info: Info;
  global_xml?: string;
  rounds: Round[];
  files?: { name: string; hash: string }[];
  import_issues?: Issue[];
}
export interface Issue {
  severity: string;
  code: string;
  path: string;
  message: string;
}
export interface Draft {
  card: Card;
  document: PackDocument;
  assets: { name: string; key: string; content_type: string; size: number }[];
  original_key: string;
  issues: Issue[] | null;
}
export interface Credentials {
  room_id: string;
  participant_id: string;
  token: string;
  invite_token?: string;
}
export interface Board {
  title: string;
  theme: string;
  pack_name: string;
  rounds: {
    id: string;
    name: string;
    type: string;
    themes: {
      id: string;
      name: string;
      questions: { id: string; value: number; played: boolean }[];
    }[];
  }[];
}
export interface Report {
  id: string;
  pack_id: string;
  reporter_id: string;
  reason: string;
  status: string;
  revision: number;
  created_at: string;
}
export interface Token {
  id: string;
  name: string;
  scopes: string[];
  created_at: string;
  expires_at: string;
  revoked_at?: string;
}
export interface Upload {
  id: string;
  offset: number;
  size: number;
  status: string;
  draft_id?: string;
  error?: string;
}
