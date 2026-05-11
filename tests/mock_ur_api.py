#!/usr/bin/env python3
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def _read_json(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length) if length > 0 else b"{}"
        if not body:
            return {}
        return json.loads(body.decode("utf-8"))

    def _write(self, payload, status=200):
        data = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, format, *args):
        return

    def do_POST(self):
        path = self.path
        body = self._read_json()
        if path == "/api/v1/system/user/self/login":
            self._write(
                {
                    "code": 200,
                    "msg": "ok",
                    "data": {
                        "token": {
                            "accessToken": "mock-session-token",
                            "accessExpire": "4102444800",
                        },
                        "info": {
                            "userID": "1001",
                            "userName": "administrator",
                            "nickName": "Mock Admin",
                            "tenants": [
                                {
                                    "tenantCode": "default",
                                    "tenant": {"name": "Default Tenant"},
                                }
                            ],
                        },
                    },
                }
            )
            return
        if path == "/api/v1/system/user/self/get-one":
            self._write(
                {
                    "code": 200,
                    "msg": "ok",
                    "data": {
                        "userID": "1001",
                        "userName": "administrator",
                        "nickName": "Mock Admin",
                        "tenants": [
                            {
                                "tenantCode": "default",
                                "isTenantOwner": 1,
                                "roles": [{"code": "admin"}],
                            }
                        ],
                        "withTenant": bool(body.get("withTenant")),
                    },
                }
            )
            return
        if path == "/api/v1/system/common/sys-config/core/get-one":
            self._write(
                {
                    "code": 200,
                    "msg": "ok",
                    "data": {"oem": {"title": "Mock UnitedRhino"}},
                }
            )
            return

        self._write({"code": 404, "msg": f"unknown path: {path}", "data": {}}, status=404)


def main():
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 19091
    server = HTTPServer(("0.0.0.0", port), Handler)
    server.serve_forever()


if __name__ == "__main__":
    main()
