#!/usr/bin/env python3
"""Edge host for the marketing build, with clean URLs.

Full split (edge/wrangler.toml): rekam.net serves only this static build —
no app routing, no proxy. The one behaviour the real deployment has that a
plain `http.server` does not is clean URLs: the edge Worker's static assets
serve /terms from terms.html (Cloudflare's default html_handling), so the
tests exercise the URL the deployment actually hands out, not /terms.html.

An optional third argument still puts this in proxy mode, for anything that
specifically wants to test against a one-origin shape — nothing in the
regular suite passes it anymore, since the marketing build's own links
(login, signup, docs) already point straight at an app origin baked in at
build time (REKAM_APP_ORIGIN — see tests/visual/run.sh and
frontend/vite.marketing.config.js), not at a path this host would need to
forward.
"""
import functools
import http.client
import http.server
import os
import sys
from urllib.parse import urlsplit


class EdgeHandler(http.server.SimpleHTTPRequestHandler):
    app_origin = None  # (host, port) when proxying, else None

    def translate_path(self, path):
        local = super().translate_path(path)
        if os.path.isdir(local) or os.path.exists(local):
            return local
        with_html = local + ".html"
        return with_html if os.path.exists(with_html) else local

    # A path is a marketing file if it resolves to one, including the clean-URL
    # form Pages serves and the index.html a directory falls back to. Anything
    # else belongs to the app.
    def _serves_static(self, path):
        local = super().translate_path(path)
        if os.path.isfile(local):
            return True
        if os.path.isdir(local):
            return os.path.isfile(os.path.join(local, "index.html"))
        return os.path.isfile(local + ".html")

    def _dispatch(self):
        if self.app_origin and not self._serves_static(self.path):
            return self._proxy()
        if self.command == "GET":
            return super().do_GET()
        if self.command == "HEAD":
            return super().do_HEAD()
        self.send_error(501, "method not supported for static paths")

    do_GET = do_HEAD = do_POST = do_PUT = do_PATCH = do_DELETE = do_OPTIONS = _dispatch

    def _proxy(self):
        host, port = self.app_origin
        conn = http.client.HTTPConnection(host, port, timeout=30)
        body = None
        length = self.headers.get("Content-Length")
        if length:
            body = self.rfile.read(int(length))
        # Strip hop-by-hop and encoding headers: the upstream may gzip, and a
        # forwarded Content-Length would only fight the body we rewrite below.
        drop = {"host", "connection", "accept-encoding", "content-length", "transfer-encoding"}
        headers = {k: v for k, v in self.headers.items() if k.lower() not in drop}
        conn.request(self.command, self.path, body=body, headers=headers)
        resp = conn.getresponse()
        data = resp.read()
        self.send_response(resp.status)
        for key, value in resp.getheaders():
            if key.lower() in {"connection", "transfer-encoding", "content-encoding", "content-length"}:
                continue
            self.send_header(key, value)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(data)
        conn.close()

    def log_message(self, fmt, *args):  # keep the harness output readable
        pass


def main():
    root, port = sys.argv[1], int(sys.argv[2])
    app_origin = None
    if len(sys.argv) > 3 and sys.argv[3]:
        parts = urlsplit(sys.argv[3])
        app_origin = (parts.hostname, parts.port or 80)
    EdgeHandler.app_origin = app_origin
    handler = functools.partial(EdgeHandler, directory=root)
    http.server.ThreadingHTTPServer(("127.0.0.1", port), handler).serve_forever()


if __name__ == "__main__":
    main()
