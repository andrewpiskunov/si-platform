#!/usr/bin/env python3
"""Collect public SI Browser metadata; prepare authorized local SIQ imports.

No archive download or publication. Python 3.10+, standard library only.
"""
import argparse
import hashlib
from html.parser import HTMLParser
import json
import os
from pathlib import Path
import re
import sys
import time
from urllib.error import HTTPError
from urllib.parse import urljoin, urlsplit
from urllib.request import Request, build_opener, HTTPRedirectHandler

ORIGIN = 'https://www.sibrowser.ru'
AGENT = 'SIPlatformCatalogue'


def safe_url(url):
    u = urlsplit(url)
    if (u.scheme != 'https' or u.netloc != 'www.sibrowser.ru' or u.fragment
            or not re.fullmatch(r'/(?:robots\.txt|packages/[0-9]+)?', u.path)
            or (u.query and (u.path != '/' or not re.fullmatch(r'page=[1-9][0-9]*', u.query)))):
        raise ValueError('URL outside allowed metadata routes')
    return url


class Robots:
    """Robots groups and wildcard/longest-match rules (fail closed on unknown rules)."""
    def __init__(self, text):
        groups, agents, rules = [], [], []
        for line in text.splitlines() + ['User-agent: __end__']:
            line = line.split('#', 1)[0].strip()
            if ':' not in line:
                continue
            key, value = (x.strip() for x in line.split(':', 1))
            key = key.lower()
            if key == 'user-agent':
                if rules:
                    groups.append((agents, rules))
                    agents, rules = [], []
                agents.append(value.lower())
            elif agents and key in ('allow', 'disallow', 'crawl-delay'):
                rules.append((key, value))
        specific = [r for a, r in groups if any(x != '*' and x in AGENT.lower() for x in a)]
        chosen = specific or [r for a, r in groups if '*' in a]
        self.rules = [r for group in chosen for r in group]
        self.delay = max([2.0] + [float(v) for k, v in self.rules if k == 'crawl-delay'])

    def allowed(self, path):
        matches = []
        for key, value in self.rules:
            if key == 'crawl-delay' or not value:
                continue
            pattern = re.escape(value.rstrip('$')).replace(r'\*', '.*')
            if value.endswith('$'):
                pattern += '$'
            if re.match(pattern, path):
                matches.append((len(value.replace('*','')), key == 'allow'))
        return max(matches)[1] if matches else True


class Node:
    def __init__(self, tag='', attrs=()):
        self.tag, self.attrs, self.children = tag, dict(attrs), []

    def text(self):
        return ' '.join(' '.join(c.text() if isinstance(c, Node) else c for c in self.children).split())

    def all(self):
        yield self
        for c in self.children:
            if isinstance(c, Node):
                yield from c.all()


class Tree(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.root = Node()
        self.stack = [self.root]

    def handle_starttag(self, tag, attrs):
        n = Node(tag, attrs)
        self.stack[-1].children.append(n)
        if tag not in {'area','base','br','col','embed','hr','img','input','link','meta','param','source','track','wbr'}:
            self.stack.append(n)

    def handle_endtag(self, tag):
        for i in range(len(self.stack)-1, 0, -1):
            if self.stack[i].tag == tag:
                del self.stack[i:]
                break

    def handle_data(self, data):
        self.stack[-1].children.append(data)


def parse_page(html):
    tree = Tree()
    tree.feed(html)
    items, next_url = [], None
    for n in tree.root.all():
        if n.tag == 'a' and 'next' in n.attrs.get('rel','').split():
            next_url = safe_url(urljoin(ORIGIN, n.attrs.get('href','')))
        if n.tag != 'article':
            continue
        nodes = list(n.all())
        def prop(name):
            return next((x for x in nodes if name in x.attrs.get('itemprop','').split()), Node())
        title = prop('headline')
        link = next((x.attrs['href'] for x in title.all() if x.tag == 'a' and 'href' in x.attrs), '')
        if not re.fullmatch(r'/packages/[0-9]+', link) or not title.text():
            raise ValueError('unrecognized package card; checkpoint not advanced')
        rounds_text, media, categories = '', {}, {}
        for element in nodes:
            headings = [c for c in element.children if isinstance(c, Node) and c.tag == 'h2' and c.text() == 'Темы в раундах']
            if headings:
                tail = element.children[element.children.index(headings[0])+1:]
                rounds_text = ' '.join(' '.join(c.text() if isinstance(c, Node) else c for c in tail).split())
            if element.tag == 'table':
                is_category = False
                for row in (c for c in element.all() if c.tag == 'tr'):
                    cells = [c.text() for c in row.all() if c.tag == 'td']
                    if cells == ['Категории']:
                        is_category = True
                    for i in range(0, len(cells)-1, 2):
                        if re.fullmatch(r'\d+%', cells[i+1]):
                            (categories if is_category else media)[cells[i]] = int(cells[i+1][:-1])
        questions = re.search(r'\b(\d+)\s+вопрос', n.text())
        items.append({'external_id':link.rsplit('/',1)[1], 'source_url':ORIGIN+link,
                      'title':title.text(), 'author':prop('author').text(),
                      'published_at':prop('datePublished').attrs.get('datetime',''),
                      'description':prop('description').text(), 'size_label':prop('contentSize').text(),
                      'tags':[x.text() for x in nodes if 'tag' in x.attrs.get('rel','').split()],
                      'question_count':int(questions[1]) if questions else None,
                      'rounds_text':rounds_text, 'media_percent':media, 'category_percent':categories,
                      'rights_status':'unverified'})
    if not items:
        raise ValueError('no recognized cards; checkpoint not advanced')
    return items, next_url


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError('redirect refused')


class Fetcher:
    def __init__(self):
        self.opener = build_opener(NoRedirect)
        self.last = 0
        self.delay = 2.0
        self.policy = None
        robots = self._get(ORIGIN+'/robots.txt')
        self.policy = Robots(robots)
        self.delay = self.policy.delay
        self.robots = robots

    def _get(self, url):
        safe_url(url)
        path = urlsplit(url).path
        if self.policy and not self.policy.allowed(path):
            raise ValueError('robots.txt denies path')
        for attempt in range(3):
            time.sleep(max(0, self.delay-(time.monotonic()-self.last)))
            self.last = time.monotonic()
            try:
                with self.opener.open(Request(url, headers={'User-Agent':AGENT+'/1.0', 'Accept':'text/html,text/plain'}), timeout=30) as response:
                    raw = response.read(4*1024*1024+1)
                    if len(raw) > 4*1024*1024:
                        raise ValueError('response too large')
                    return raw.decode('utf-8')
            except HTTPError as error:
                if error.code not in (429,500,502,503,504) or attempt == 2:
                    raise
                # Long server delays stop the run for an operator-controlled retry.
                retry = error.headers.get('Retry-After', '')
                if retry and (not retry.isdigit() or int(retry) > 60):
                    raise
                time.sleep(max(2**attempt, int(retry or '0')))
            except OSError:
                if attempt == 2:
                    raise
                time.sleep(2**attempt)

    def __call__(self, url):
        return self._get(url)


def atomic_json(path, value):
    temporary = path.with_suffix(path.suffix+'.tmp')
    temporary.write_text(json.dumps(value, ensure_ascii=False, indent=2)+'\n', encoding='utf-8')
    os.replace(temporary, path)


def collect(directory, pages, fetch):
    directory.mkdir(parents=True, exist_ok=True)
    path = directory/'state.json'
    state = json.loads(path.read_text()) if path.exists() else {'version':1,'next_url':ORIGIN+'/', 'items':{},'pages':0}
    if state.get('version') != 1:
        raise ValueError('unsupported checkpoint version')
    for _ in range(pages):
        url = state['next_url']
        if url is None:
            break
        safe_url(url)
        try:
            items, next_url = parse_page(fetch(url))
            if next_url == url:
                raise ValueError('pagination loop')
            for item in items:
                state['items'][item['external_id']] = item
            state['next_url'] = next_url
            state['pages'] += 1
            atomic_json(path, state)
            event = {'url':url,'status':'ok','items':len(items)}
        except Exception as error:
            with (directory/'events.jsonl').open('a') as log:
                log.write(json.dumps({'url':url,'status':'error','error':type(error).__name__})+'\n')
            raise
        with (directory/'events.jsonl').open('a') as log:
            log.write(json.dumps(event)+'\n')
    return state


def build_manifest(items, approvals, directory):
    lookup = {i['external_id']:i for i in items}
    result, hashes, ids = [], set(), set()
    if not 1 <= len(approvals) <= 100:
        raise ValueError('approval batch must contain 1–100 entries')
    for a in approvals:
        key = str(a['external_id'])
        if key not in lookup or key in ids:
            raise ValueError('unknown or duplicate external_id')
        item = lookup[key]
        if not a.get('license','').strip() or not a.get('permission_basis','').strip():
            raise ValueError('explicit license and permission_basis required')
        name = a['file']
        if Path(name).is_absolute() or '..' in Path(name).parts:
            raise ValueError('SIQ path must be relative to manifest directory')
        target = (directory/name).resolve()
        if not target.is_relative_to(directory.resolve()) or not target.is_file() or target.suffix.lower() != '.siq':
            raise ValueError('SIQ must be a regular file inside manifest directory')
        if target.stat().st_size > 250*1024*1024:
            raise ValueError('SIQ exceeds 250 MiB')
        digest = hashlib.sha256()
        with target.open('rb') as f:
            for block in iter(lambda:f.read(65536), b''):
                digest.update(block)
        checksum = digest.hexdigest()
        if checksum in hashes:
            raise ValueError('duplicate archive checksum')
        hashes.add(checksum)
        ids.add(key)
        result.append({'namespace':'sibrowser.ru','external_id':key,'file':name,'sha256':checksum,
                       'source_url':safe_url(item['source_url']), 'license':a['license'],
                       'permission_basis':a['permission_basis'],'attribution':a.get('attribution') or item['author']})
    return {'items':result}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    scan = commands.add_parser('collect', help='metadata-only dry-run; resumes checkpoint')
    scan.add_argument('--output', type=Path, required=True)
    scan.add_argument('--pages', type=int, default=1, help='additional pages, 1–100; default 1')
    prepare = commands.add_parser('manifest', help='prepare authorized LOCAL files for catalogue-import')
    prepare.add_argument('--state', type=Path, required=True)
    prepare.add_argument('--approvals', type=Path, required=True)
    prepare.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    if args.command == 'collect':
        if not 1 <= args.pages <= 100:
            parser.error('--pages must be 1–100')
        args.output.mkdir(parents=True, exist_ok=True)
        fetch = Fetcher()
        (args.output/'robots.txt').write_text(fetch.robots)
        state = collect(args.output,args.pages,fetch)
        print(json.dumps({'items':len(state['items']),'pages':state['pages'],'next_url':state['next_url'],'archives_downloaded':0}))
    else:
        state = json.loads(args.state.read_text())
        approvals = json.loads(args.approvals.read_text())
        result = build_manifest(state['items'].values(), approvals['items'], args.output.parent)
        atomic_json(args.output,result)
        print(json.dumps({'prepared':len(result['items']),'published':0}))


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, KeyError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
