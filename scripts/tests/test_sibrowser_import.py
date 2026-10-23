import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from urllib.error import HTTPError
import io
from http.server import HTTPServer, BaseHTTPRequestHandler
import threading

spec = importlib.util.spec_from_file_location('sibrowser', Path(__file__).parents[1] / 'sibrowser_import.py')
si = importlib.util.module_from_spec(spec)
spec.loader.exec_module(si)

PAGE = '''<a rel="next" href="/?page=2">Next</a><article itemtype="http://schema.org/Article"><span itemprop="author"><span itemprop="name">Alice &amp; Bob</span></span><time itemprop="datePublished" datetime="2026-10-22"></time><h1 itemprop="name headline"><a href="/packages/42">Quiz</a></h1><p itemprop="description">A <b>good</b> quiz</p><span itemprop="contentSize">12 КБ</span><a rel="tag">General</a><table><tr><td>10 вопросов</td></tr></table></article>'''

class ImportTests(unittest.TestCase):
    def test_parser_preserves_source_and_author_without_downloads(self):
        items, next_url = si.parse_page(PAGE)
        self.assertEqual(next_url, 'https://www.sibrowser.ru/?page=2')
        self.assertEqual(items[0]['external_id'], '42')
        self.assertEqual(items[0]['author'], 'Alice & Bob')
        self.assertEqual(items[0]['title'], 'Quiz')
        self.assertEqual(items[0]['description'], 'A good quiz')
        self.assertEqual(items[0]['tags'], ['General'])
        self.assertEqual(items[0]['question_count'], 10)
        self.assertEqual(items[0]['rights_status'], 'unverified')

    def test_rounds_and_distributions_remain_source_metadata(self):
        page = PAGE.replace('<table>', '<div><h2>Темы в раундах</h2>Round 1:<span>Music, Art</span><br>Final:<span>History</span></div><table><tr><td>Фото</td><td>25%</td><td>Звук</td><td>10%</td></tr><tr><td>Категории</td></tr><tr><td>Кино</td><td>20%</td></tr>')
        items, _ = si.parse_page(page)
        self.assertEqual(items[0]['rounds_text'], 'Round 1: Music, Art Final: History')
        self.assertEqual(items[0]['media_percent'], {'Фото':25, 'Звук':10})
        self.assertEqual(items[0]['category_percent'], {'Кино':20})

    def test_robots_wildcard_and_explicit_agent(self):
        policy = si.Robots('User-agent: *\nDisallow: */direct_download\nUser-agent: SIPlatformCatalogue\nDisallow: /private\n')
        self.assertFalse(policy.allowed('/private/1'))
        # Never fetch downloads even if a future robots change permits them.
        for url in ['https://evil.test/', 'https://www.sibrowser.ru/packages/42/direct_download', 'https://www.sibrowser.ru/packages/42/direct_play']:
            with self.assertRaises(ValueError): si.safe_url(url)
        self.assertFalse(si.Robots('User-agent: *\nDisallow: */direct_download').allowed('/packages/42/direct_download'))

    def test_resume_retries_failed_page_and_deduplicates(self):
        with tempfile.TemporaryDirectory() as tmp:
            calls = []
            def failing(url):
                calls.append(url)
                if 'page=2' in url: raise OSError('offline')
                return PAGE
            with self.assertRaises(OSError): si.collect(Path(tmp), 2, failing)
            state = json.loads((Path(tmp) / 'state.json').read_text())
            self.assertEqual(len(state['items']), 1)
            self.assertEqual(state['next_url'], 'https://www.sibrowser.ru/?page=2')
            si.collect(Path(tmp), 1, lambda url: PAGE.replace('<a rel="next" href="/?page=2">Next</a>', ''))
            state = json.loads((Path(tmp) / 'state.json').read_text())
            self.assertEqual(len(state['items']), 1)
            self.assertIsNone(state['next_url'])
            self.assertIn('error', (Path(tmp) / 'events.jsonl').read_text())

    def test_fetch_retries_transient_errors_and_checks_robots_before_request(self):
        class Opener:
            calls = 0
            def open(self, req, timeout):
                self.calls += 1
                if req.full_url.endswith('/robots.txt'):
                    return io.BytesIO(b'User-agent: *\nDisallow: /packages/\n')
                if self.calls == 2:
                    raise HTTPError(req.full_url, 503, 'Unavailable', {}, None)
                return io.BytesIO(PAGE.encode())
        opener = Opener()
        with patch.object(si, 'build_opener', return_value=opener), patch.object(si.time, 'sleep'):
            fetch = si.Fetcher()
            self.assertIn('Quiz', fetch(si.ORIGIN+'/'))
            self.assertEqual(opener.calls, 3)
            with self.assertRaises(ValueError): fetch(si.ORIGIN+'/packages/42')
            self.assertEqual(opener.calls, 3)

    def test_redirect_is_refused_before_following_location(self):
        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(302)
                self.send_header('Location', 'https://www.sibrowser.ru/packages/42/direct_download')
                self.end_headers()
            def log_message(self, *args): pass
        server = HTTPServer(('127.0.0.1', 0), Handler)
        worker = threading.Thread(target=server.serve_forever, daemon=True)
        worker.start()
        try:
            opener = si.build_opener(si.NoRedirect)
            with self.assertRaisesRegex(ValueError, 'redirect refused'):
                opener.open('http://127.0.0.1:'+str(server.server_port)+'/', timeout=2)
        finally:
            server.shutdown()
            server.server_close()
            worker.join()

    def test_manifest_requires_rights_and_rejects_duplicate_hashes(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root/'own.siq').write_bytes(b'own fixture')
            items, _ = si.parse_page(PAGE)
            approval = {'external_id':'42','file':'own.siq','license':'CC0','permission_basis':'Own work'}
            manifest = si.build_manifest(items, [approval], root)
            self.assertEqual(manifest['items'][0]['source_url'], 'https://www.sibrowser.ru/packages/42')
            self.assertEqual(len(manifest['items'][0]['sha256']),64)
            self.assertEqual(manifest['items'][0]['attribution'],'Alice & Bob')
            with self.assertRaises(ValueError): si.build_manifest(items, [{**approval,'permission_basis':''}],root)
            with self.assertRaises(ValueError): si.build_manifest(items,[{**approval,'file':'../other.siq'}],root)
            with self.assertRaises(ValueError): si.build_manifest(items,[approval,approval],root)
            with self.assertRaises(ValueError): si.build_manifest(items,[{**approval,'file':str(root/'own.siq')}],root)

if __name__ == '__main__': unittest.main()
